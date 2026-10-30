package worker

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// promptGuard wraps the MCP caller of one prompt-task execution. The worker, not
// the model, decides what the task's commits and log lines say:
//
//   - reason: the server refuses an argument a tool's schema does not declare
//     (internal/mcpserver/sdkbridge.go), and declares `reason` on exactly the
//     committing tools, so the guard sets it only where the schema offered by
//     the server declares it. The task name goes first: the server cuts a reason
//     at 500 bytes.
//   - log_append: the log is one file where any "[path] " prefix and any "## "
//     line forges an entry. A model's line could otherwise fake a worker/ budget
//     or cadence entry, or write kb-doctor, which moves last_doctor. The path is
//     forced to task/<name> and an entry that could forge is refused locally.
//
// Everything else is forwarded byte for byte. Allowlist and deny set are the
// loop's business and run before this.
type promptGuard struct {
	inner ToolCaller
	name  string

	mu      sync.Mutex
	reason  map[string]bool // tools whose offered schema declares "reason"
	refused int
}

func newPromptGuard(inner ToolCaller, name string) *promptGuard {
	return &promptGuard{inner: inner, name: name, reason: map[string]bool{}}
}

// ListTools records which offered tools declare a reason.
func (g *promptGuard) ListTools() ([]client.ToolInfo, error) {
	tools, err := g.inner.ListTools()
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range tools {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(t.InputSchema, &s) == nil {
			if _, ok := s.Properties["reason"]; ok {
				g.reason[t.Name] = true
			}
		}
	}
	return tools, nil
}

// Refused is how many log_append calls the guard turned away.
func (g *promptGuard) Refused() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refused
}

// forgesLog reports whether a model's log entry could pass for another entry.
func forgesLog(entry string) bool {
	if strings.Contains(entry, "kb-doctor") {
		return true
	}
	for _, line := range strings.Split(entry, "\n") {
		if strings.HasPrefix(line, "## ") {
			return true
		}
	}
	return false
}

// CallTool applies the rules above.
func (g *promptGuard) CallTool(name string, args any) (client.ToolOutput, error) {
	g.mu.Lock()
	wantReason := g.reason[name]
	g.mu.Unlock()
	if !wantReason && name != "log_append" {
		return g.inner.CallTool(name, args)
	}
	raw, ok := args.(json.RawMessage)
	if !ok {
		b, err := json.Marshal(args)
		if err != nil {
			return g.inner.CallTool(name, args)
		}
		raw = b
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return g.inner.CallTool(name, args)
	}
	if name == "log_append" {
		var entry string
		if json.Unmarshal(obj["entry"], &entry) == nil && forgesLog(entry) {
			g.mu.Lock()
			g.refused++
			g.mu.Unlock()
			return client.ToolOutput{IsError: true, Text: "log entry refused: it may not contain a line starting with \"## \" or the text kb-doctor"}, nil
		}
		obj["path"], _ = json.Marshal("task/" + g.name)
	}
	if wantReason {
		reason := "prompt task " + g.name
		var given string
		if json.Unmarshal(obj["reason"], &given) == nil && strings.TrimSpace(given) != "" {
			reason += ": " + given
		}
		obj["reason"], _ = json.Marshal(reason)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return g.inner.CallTool(name, args)
	}
	return g.inner.CallTool(name, json.RawMessage(out))
}
