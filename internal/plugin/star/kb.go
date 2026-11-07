package star

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// KBCaller is what kb.call reaches: the KB's tools through one adapter per
// surface (the command line and the bridge). Both must be built on the same
// read-only decision, the server's own annotation: see MCPKB.
type KBCaller interface {
	// Call runs a tool and returns the text of its result; a tool that
	// answers with an error returns it as the error.
	Call(ctx context.Context, tool string, args map[string]any) (string, error)
	// ReadOnly reports whether the server annotates the tool read-only. An
	// unknown tool is false, never an error: absent annotation means write.
	ReadOnly(ctx context.Context, tool string) (bool, error)
}

// kbState is the KB access of one call.
type kbState struct {
	cc       *callCtx
	caller   KBCaller
	readOnly bool
	// why names what made the call read-only when it is not the session.
	why string
}

func kbModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "kb", Members: starlark.StringDict{
		"call": starlark.NewBuiltin("kb.call", kbCall),
	}}
}

// kbCall is kb.call(tool, **args), or kb.call(tool, {args}). The gate is the
// plugin's declared limit, not the security boundary: the server still holds
// the token's scopes and the session's access.
func kbCall(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	c, err := callPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("kb.call: missing the tool name")
	}
	tool, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("kb.call: the tool name must be a string, not %s", args[0].Type())
	}
	var named *starlark.Dict
	switch len(args) {
	case 1:
	case 2:
		d, ok := args[1].(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("kb.call: the second positional argument must be a dict, not %s", args[1].Type())
		}
		named = d
	default:
		return nil, errors.New("kb.call: takes the tool name and at most one dict of arguments")
	}
	mode := ""
	if c.perms != nil {
		mode = c.perms.KB
	}
	if mode != "read" && mode != "write" {
		return nil, denied("kb.call needs permissions.kb in the manifest")
	}
	k := c.kb
	if k.readOnly {
		mode = "read" // a read-only session is read whatever the manifest says
	}
	if k.caller == nil {
		return nil, errors.New("kb.call: no KB is reachable from here")
	}
	// The gate decides on the exact name the server will receive: no
	// normalisation, so a different case or a padded name is an unknown tool.
	if mode == "read" {
		ro, err := k.caller.ReadOnly(c.ctx, tool)
		if err != nil {
			return nil, fmt.Errorf("kb.call: %s", client.Escape(err.Error()))
		}
		if !ro {
			why := "this session is read-only"
			if k.why != "" {
				why = k.why
			}
			if !k.readOnly {
				why = "permissions.kb is read"
			}
			return nil, denied("kb.call: %q is not a read-only tool and %s", tool, why)
		}
	}

	argv := map[string]any{}
	if named != nil {
		m, err := jsonArgs(c.ctx, named)
		if err != nil {
			return nil, err
		}
		argv = m
	}
	if len(kwargs) > 0 {
		kd := starlark.NewDict(len(kwargs))
		for _, kv := range kwargs {
			_ = kd.SetKey(kv[0], kv[1])
		}
		m, err := jsonArgs(c.ctx, kd)
		if err != nil {
			return nil, err
		}
		for key, v := range m {
			if _, dup := argv[key]; dup {
				return nil, fmt.Errorf("kb.call: argument %q is given twice", key)
			}
			argv[key] = v
		}
	}

	text, err := k.caller.Call(c.ctx, tool, argv)
	if err != nil {
		return nil, fmt.Errorf("kb.call: %s", client.Escape(err.Error()))
	}
	if len(text) > MaxResultBytes {
		return nil, limited("kb.call: the result of %q is over the %d byte cap", tool, MaxResultBytes)
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) == nil && !dec.More() {
		if sv, err := toStarlark(v); err == nil {
			return sv, nil
		}
	}
	return starlark.String(text), nil
}

// jsonArgs turns a Starlark dict into a tool's arguments; keys must be strings.
func jsonArgs(ctx context.Context, d *starlark.Dict) (map[string]any, error) {
	for _, k := range d.Keys() {
		if _, ok := k.(starlark.String); !ok {
			return nil, fmt.Errorf("kb.call: argument names must be strings, not %s", k.Type())
		}
	}
	var buf bytes.Buffer
	if err := encodeJSON(ctx, &buf, d, 0); err != nil {
		var ce *CallError
		if errors.As(err, &ce) {
			return nil, fmt.Errorf("kb.call: arguments: %s", ce.Msg)
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("kb.call: arguments: %v", err)
	}
	return m, nil
}

// ToolClient is the part of client.MCPClient that MCPKB uses.
type ToolClient interface {
	CallContext(ctx context.Context, tool string, args any) (json.RawMessage, error)
	ListToolsContext(ctx context.Context) ([]client.ToolInfo, error)
}

// toolsTTL is how long the read-only classification is trusted: a bridge lives
// as long as its session, and the server's tools can change under it.
const toolsTTL = 5 * time.Minute

// toolsMinRefresh bounds the refetch for an unknown name, so a script cannot
// turn a loop of made-up names into a request each.
const toolsMinRefresh = 5 * time.Second

// MCPKB is the KBCaller over an MCP client: both surfaces build it, so the
// read-only decision (the server's tools/list annotation, kept for toolsTTL)
// exists once. The script passes `kb` itself, as every routed tool requires; it
// is not restricted here, the server's token scopes and session KB set are.
type MCPKB struct {
	c   ToolClient
	now func() time.Time // a seam of tests

	mu      sync.Mutex
	tools   map[string]bool // name -> read-only; nil until fetched
	fetched time.Time
}

func NewMCPKB(c ToolClient) *MCPKB { return &MCPKB{c: c, now: time.Now} }

// refresh loads the list; the caller holds mu. A failure leaves the previous
// list in place and is returned, so it is never cached as an answer.
func (m *MCPKB) refresh(ctx context.Context) error {
	list, err := m.c.ListToolsContext(ctx)
	if err != nil {
		return err
	}
	m.tools = make(map[string]bool, len(list))
	for _, t := range list {
		m.tools[t.Name] = t.ReadOnly
	}
	m.fetched = m.now()
	return nil
}

// ReadOnly answers from the list, refetching it when it is older than toolsTTL
// or does not know the tool (a tool added since); a tool still unknown after a
// fresh fetch is a write.
func (m *MCPKB) ReadOnly(ctx context.Context, tool string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh := false
	if m.tools == nil || m.now().Sub(m.fetched) > toolsTTL {
		if err := m.refresh(ctx); err != nil {
			return false, err
		}
		fresh = true
	}
	ro, known := m.tools[tool]
	if !known && !fresh && m.now().Sub(m.fetched) >= toolsMinRefresh {
		if err := m.refresh(ctx); err != nil {
			return false, err
		}
		ro = m.tools[tool]
	}
	return ro, nil
}

// Call abandons the request when the call's context ends, so a write is not
// left running after the script was told the call failed.
func (m *MCPKB) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	raw, err := m.c.CallContext(ctx, tool, args)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
