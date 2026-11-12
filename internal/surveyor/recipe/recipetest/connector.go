package recipetest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
)

// truncatedMark is what the connector set appends to a result it cut.
const truncatedMark = "\n[truncated]"

// toolNameRE is the grammar of the tool names model providers accept: letters,
// digits, underscore and hyphen, at most connector.MaxToolNameLen. The
// Surveyor's own connector set checks only the length, so the kit holds the
// offered name (alias, separator and tool) to the whole grammar: a tool named
// "get.page" would be offered and then refused by the model API.
var toolNameRE = regexp.MustCompile(fmt.Sprintf("^[a-zA-Z0-9_-]{1,%d}$", connector.MaxToolNameLen))

// ConnectorInput is one connector to test: the Config the Surveyor's own plugin
// loader built, and the scenario whose calls are made through it.
type ConnectorInput struct {
	Name     string // the local name, the one tests/<name>.yaml is named after
	Config   connector.Config
	Env      map[string]string // NAME the plugin declares -> the variable the Config reads it from
	Scenario *Scenario
}

// RunConnector opens the connector the way a Surveyor does (connector.Open, the
// descriptor's allowlist, the call timeout and the result cap), makes each call of
// the scenario and judges what the server offered. A stdio connector is the
// author's process; an http one is a live server, which a test never reaches
// (its calls are still held to the descriptor's allowlist).
//
// The checks of the connector row: (1) it starts and lists its tools, (2) every
// tool of the allowlist is offered within the schema and name caps (the converse,
// a tool offered that the allowlist lacks, is enforced by Set.connect, so the kit
// only sees the allowlisted ones), (3) the offered names are valid after
// aliasing, (4) a call outside the allowlist is refused, (5) the result cap holds
// (Set.Call enforces it; the kit confirms it), (6) the child sees only the
// variables the descriptor lists (connector.ChildEnv enforces it in production;
// the kit's own test pins it, no scenario can), and (7) no env test value is in a result or an error.
func RunConnector(ctx context.Context, in ConnectorInput) []Result {
	x := &run{in: Input{Name: in.Name, Scenario: in.Scenario}, env: in.Env}
	cfg := in.Config
	if cfg.Transport != connector.TransportStdio {
		x.add("connector", nil, "an http connector is a live server, which plugin test never reaches")
		for i, c := range in.Scenario.Calls {
			check := fmt.Sprintf("call %d %s", i+1, c.Tool)
			if !slices.Contains(cfg.Tools, c.Tool) && !c.ExpectError {
				x.add(check, fmt.Errorf("the tool is not in the connector's allowlist, so a Surveyor would refuse it"), "")
				continue
			}
			x.add(check, nil, "not made: an http connector is a live server")
		}
		return x.out
	}
	defer x.setEnv(in.Env)()
	var logs []string
	set := connector.Open(ctx, []connector.Config{cfg}, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	defer set.Close()

	var probs []string
	for i, c := range in.Scenario.Calls {
		res, err := set.Call(ctx, connector.Alias(cfg.Name)+connector.Separator+c.Tool, c.Args)
		x.add(fmt.Sprintf("call %d %s", i+1, c.Tool), judgeCall(c, res, err), "")
		x.note("call "+c.Tool, res.Text)
		if err != nil {
			x.note("the error of call "+c.Tool, err.Error())
		}
		if len(res.Text) > cfg.MaxResultBytes+len(truncatedMark) {
			probs = append(probs, fmt.Sprintf("call %d: the result is over max_result_bytes", i+1))
		}
	}
	tools, err := set.Tools(ctx, cfg.Name)
	if err != nil {
		probs = append(probs, err.Error())
		x.note("the start error", err.Error())
	}
	offered := map[string]bool{}
	for _, t := range tools {
		offered[strings.TrimPrefix(t.Name, connector.Alias(cfg.Name)+connector.Separator)] = true
		if !toolNameRE.MatchString(t.Name) {
			probs = append(probs, fmt.Sprintf("the offered tool name %q is not valid: model providers accept %s", t.Name, toolNameRE))
		}
	}
	for _, name := range cfg.Tools {
		if err != nil || offered[name] {
			continue
		}
		// The set drops a tool with an oversize schema or a name over the cap and
		// says why on its log; a tool the server does not have is simply absent.
		why := "the server does not offer it"
		if i := slices.IndexFunc(logs, func(l string) bool { return strings.Contains(l, "tool "+name+" not offered") }); i >= 0 {
			why = logs[i]
		}
		probs = append(probs, fmt.Sprintf("tool %s is in the allowlist but is not offered: %s", name, why))
	}
	for _, l := range logs { // why a start failed, as the set words it
		x.note("the connector log", l)
	}
	for n, v := range in.Scenario.Env {
		if len(v) >= 4 && !in.Scenario.fixturePaths[n] && slices.ContainsFunc(x.seen, func(s [2]string) bool { return strings.Contains(s[1], v) }) {
			probs = append(probs, "the value of "+n+" appears in a call result or error")
		}
	}
	slices.Sort(probs)
	x.verdict("connector", probs)
	return x.out
}

func judgeCall(c Call, res connector.Result, err error) error {
	switch {
	case err != nil && c.ExpectError:
		return nil
	case err != nil:
		return err
	case res.IsError != c.ExpectError:
		return fmt.Errorf("the result is an error: %t, expected %t", res.IsError, c.ExpectError)
	case c.ExpectTextContains != "" && !strings.Contains(res.Text, c.ExpectTextContains):
		return errors.New("the result does not contain " + fmt.Sprintf("%q", c.ExpectTextContains))
	}
	return nil
}
