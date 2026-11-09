package synthesis_test

import (
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}

// fake is a scripted MCP caller: one handler per tool, every call recorded.
type fake struct {
	mu      sync.Mutex
	handler map[string]func(args map[string]any) client.ToolOutput
	calls   []call
}

type call struct {
	Tool string
	Args map[string]any
}

func (f *fake) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range []string{"search", "concept_read", "concept_write", "concept_patch", "concept_delete", "concept_merge", "artifact_write", "kb_review"} {
		out = append(out, client.ToolInfo{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return out, nil
}

func (f *fake) CallTool(name string, args any) (client.ToolOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(args)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.calls = append(f.calls, call{name, m})
	if h := f.handler[name]; h != nil {
		return h(m), nil
	}
	return client.ToolOutput{IsError: true, Text: "unscripted " + name}, nil
}

func (f *fake) count(tool string) int {
	n := 0
	for _, c := range f.calls {
		if c.Tool == tool {
			n++
		}
	}
	return n
}

func js(v any) client.ToolOutput {
	b, _ := json.Marshal(v)
	return client.ToolOutput{Text: string(b)}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
