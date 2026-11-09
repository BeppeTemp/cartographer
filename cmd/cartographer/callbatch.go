package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// describeTools prints the description and input schema of the tools in spec,
// a comma-separated list: one name prints one object, several print an array
// in the order asked. Nothing is printed unless every name is listed.
func describeTools(tools []toolInfo, spec string, fail func(string, ...any) int) int {
	names := strings.Split(spec, ",")
	byName := make(map[string]toolInfo, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}
	seen := map[string]bool{}
	var unknown []string
	var picked []map[string]any
	for _, n := range names {
		if n == "" {
			return fail("--describe %q has an empty tool name", spec)
		}
		if seen[n] {
			return fail("--describe names %q twice", n)
		}
		seen[n] = true
		t, ok := byName[n]
		if !ok {
			unknown = append(unknown, fmt.Sprintf("%q", n))
			continue
		}
		picked = append(picked, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
	}
	if len(unknown) == 1 {
		return fail("unknown tool %s; run \"cartographer call --list\"", unknown[0])
	}
	if len(unknown) > 1 {
		return fail("unknown tools %s; run \"cartographer call --list\"", strings.Join(unknown, ", "))
	}
	var v any = picked
	if len(picked) == 1 && len(names) == 1 {
		v = picked[0]
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
	return 0
}

// printInstructions sends initialize through the session (built with Guide, so
// the server sends its operating guide) and prints result.instructions.
func printInstructions(ctx context.Context, s *callSession, output string) int {
	resp, err := s.Do(ctx, "initialize", map[string]any{
		"protocolVersion": client.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": client.ClientName, "version": version},
	})
	if err == nil {
		var result json.RawMessage
		if result, err = responseResult(resp); err == nil {
			var r struct {
				Instructions string `json:"instructions"`
			}
			if err = json.Unmarshal(result, &r); err != nil {
				err = fmt.Errorf("unreadable initialize result: %w", err)
			} else if strings.TrimSpace(r.Instructions) == "" {
				err = errors.New("the server sent no instructions")
			} else if output == "json" {
				var out bytes.Buffer
				_ = json.Indent(&out, result, "", "  ")
				fmt.Fprintln(os.Stdout, out.String())
				return 0
			} else {
				// KB-authored content: control characters (an ESC or OSC
				// sequence) are shown escaped, line breaks and tabs kept.
				lines := strings.Split(r.Instructions, "\n")
				for i, l := range lines {
					lines[i] = escapeControl(l)
				}
				fmt.Fprintln(os.Stdout, strings.Join(lines, "\n"))
				return 0
			}
		}
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	return 2
}

// batchRequest is one input line of --batch.
type batchRequest struct {
	tool string
	args json.RawMessage
}

// batchResult is one output line of --batch.
type batchResult struct {
	Line   int             `json:"line"`
	ID     json.RawMessage `json:"id,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	OK     bool            `json:"ok"`
	Text   *string         `json:"text,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *batchError     `json:"error,omitempty"`
}

type batchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// parseBatchLine reads one input line strictly: a JSON object with a non-empty
// string "tool", an optional object "arguments" and an optional "id", nothing
// else. The id is returned whenever the line is an object that has one.
func parseBatchLine(text string) (req batchRequest, id json.RawMessage, err error) {
	var m map[string]json.RawMessage
	if err = json.Unmarshal([]byte(text), &m); err != nil || m == nil {
		return req, nil, errors.New("not a JSON object")
	}
	id = m["id"]
	for k := range m {
		if k != "tool" && k != "arguments" && k != "id" {
			return req, id, fmt.Errorf("unknown key %q (valid: tool, arguments, id)", k)
		}
	}
	if err = json.Unmarshal(m["tool"], &req.tool); err != nil || req.tool == "" {
		return req, id, errors.New(`"tool" must be a non-empty string`)
	}
	req.args = json.RawMessage("{}")
	if a := bytes.TrimSpace(m["arguments"]); len(a) > 0 && string(a) != "null" {
		if a[0] != '{' {
			return req, id, errors.New(`"arguments" must be a JSON object`)
		}
		req.args = a
	}
	return req, id, nil
}

// batchReader yields the non-empty lines of the input with their 1-based
// number (empty lines count in the numbering).
type batchReader struct {
	sc  *bufio.Scanner
	n   int
	err error // set once the scanner failed
}

func newBatchReader(in io.Reader) *batchReader {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), maxCallJSON)
	return &batchReader{sc: sc}
}

// next returns the next non-empty line; ok is false at EOF.
func (r *batchReader) next() (n int, text string, ok bool) {
	for r.sc.Scan() {
		r.n++
		if t := strings.TrimSpace(r.sc.Text()); t != "" {
			return r.n, t, true
		}
	}
	if err := r.sc.Err(); err != nil && r.err == nil {
		r.err = err
		r.n++
	}
	return r.n, "", false
}

// runBatch is `call --batch -`: the requests of in go through one session, one
// at a time, and each result is written to out as soon as it is ready. A
// failing line never stops the batch; the exit code is the worst class among
// the lines (1 the tool or its arguments, 2 anything else). The session is
// started by start at the first request, so an empty input starts no bridge,
// and closed here; the bridge log is returned.
func runBatch(ctx context.Context, start func() *callSession, in io.Reader, out, errOut io.Writer, output string) (int, string) {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	rd := newBatchReader(in)
	n, text, ok := rd.next()
	if !ok && rd.err == nil {
		fmt.Fprintln(errOut, "Error: --batch - read no request")
		return 2, ""
	}
	var sess *callSession
	if ok {
		sess = start()
	}
	worst := 0
	emit := func(r batchResult) {
		if r.Error != nil && r.Error.Code > worst {
			worst = r.Error.Code
		}
		_ = enc.Encode(r)
	}
	fail := func(n int, id json.RawMessage, tool string, code int, msg string) {
		emit(batchResult{Line: n, ID: id, Tool: tool, Error: &batchError{Code: code, Message: escapeControl(msg)}})
	}
	for ; ok; n, text, ok = rd.next() {
		req, id, err := parseBatchLine(text)
		if cerr := ctx.Err(); cerr != nil {
			// Stop reading: the line just read is answered, the rest is dropped.
			fail(n, id, req.tool, 2, ctxMessage(cerr))
			break
		}
		if err != nil {
			fail(n, id, "", 2, fmt.Sprintf("line %d: %v", n, err))
			continue
		}
		if stopped, serr := sess.stopped(); stopped {
			fail(n, id, req.tool, 2, serr.Error())
			continue
		}
		resp, err := sess.Do(ctx, "tools/call", map[string]any{"name": req.tool, "arguments": req.args})
		if err != nil {
			fail(n, id, req.tool, 2, err.Error())
			continue
		}
		result, err := responseResult(resp)
		if err != nil {
			code := 2
			if errors.Is(err, errInvalidParams) {
				code = 1
			}
			fail(n, id, req.tool, code, err.Error())
			continue
		}
		o, err := readToolResult(req.tool, result)
		if err != nil {
			fail(n, id, req.tool, 2, err.Error())
			continue
		}
		for _, typ := range o.omitted {
			fmt.Fprintf(errOut, "[%s content omitted]\n", typ)
		}
		if o.isError {
			fail(n, id, req.tool, 1, o.text)
			continue
		}
		r := batchResult{Line: n, ID: id, Tool: req.tool, OK: true}
		if output == "json" {
			r.Result = result
		} else {
			r.Text = &o.text
		}
		emit(r)
	}
	if rd.err != nil {
		msg := fmt.Sprintf("line %d: read error: %v", rd.n, rd.err)
		if errors.Is(rd.err, bufio.ErrTooLong) {
			msg = fmt.Sprintf("line %d is larger than 16 MiB", rd.n)
		}
		fail(rd.n, nil, "", 2, msg)
	}
	log := ""
	if sess != nil {
		log = sess.Close()
	}
	return worst, log
}

// ctxMessage is the words of a single call for a deadline or an interrupt.
func ctxMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out waiting for the server"
	}
	return "interrupted"
}
