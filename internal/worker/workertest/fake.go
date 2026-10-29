// Package workertest is the deterministic test double of package worker: a
// scripted Model and a scripted MCP tool caller, with no network.
package workertest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker"
)

// Fake implements worker.Model: it returns Turns in order, records every
// Request, and fails when the script is exhausted so a loop that does not stop
// fails the test instead of hanging.
type Fake struct {
	Turns    []worker.Response
	Requests []worker.Request
	mu       sync.Mutex
}

// Complete implements worker.Model.
func (f *Fake) Complete(_ context.Context, req worker.Request) (worker.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, req)
	if len(f.Requests) > len(f.Turns) {
		return worker.Response{}, errors.New("workertest: the script has no turn left")
	}
	return f.Turns[len(f.Requests)-1], nil
}

// FakeTools implements the MCP caller interface of the loop.
type FakeTools struct {
	Tools   []client.ToolInfo
	Outputs map[string]client.ToolOutput // default {Text: "ok"}
	Errors  map[string]error             // returned instead of an output
	Calls   map[string]int               // calls per tool
	Order   []string                     // every call, in order
	Args    map[string]json.RawMessage   // last arguments per tool
	mu      sync.Mutex
}

// Listing builds a tool list from names.
func Listing(names ...string) []client.ToolInfo {
	out := make([]client.ToolInfo, len(names))
	for i, n := range names {
		out[i] = client.ToolInfo{Name: n, Description: n, InputSchema: json.RawMessage(`{"type":"object"}`)}
	}
	return out
}

// ListTools implements the caller interface.
func (f *FakeTools) ListTools() ([]client.ToolInfo, error) { return f.Tools, nil }

// CallTool implements the caller interface.
func (f *FakeTools) CallTool(name string, args any) (client.ToolOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Calls == nil {
		f.Calls, f.Args = map[string]int{}, map[string]json.RawMessage{}
	}
	f.Calls[name]++
	f.Order = append(f.Order, name)
	if raw, ok := args.(json.RawMessage); ok {
		f.Args[name] = raw
	}
	if err := f.Errors[name]; err != nil {
		return client.ToolOutput{}, err
	}
	if out, ok := f.Outputs[name]; ok {
		return out, nil
	}
	return client.ToolOutput{Text: "ok"}, nil
}
