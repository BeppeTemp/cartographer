package recipetest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pageArgs struct {
	ID string `json:"id"`
}

type noArgs struct{}

// mcpHelper is a stdio MCP server with five tools: read, write and env (which
// prints the environment the connector gave the process), big (an input schema
// over the cap the Surveyor's connector set enforces) and bad.name (a name the
// model providers refuse).
func mcpHelper() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: "reads a page"}, func(ctx context.Context, r *mcp.CallToolRequest, in pageArgs) (*mcp.CallToolResult, any, error) {
		return text("page " + in.ID), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "write", Description: "mutates"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		return text("written"), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "env", Description: "environment"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		// Only the variables a test sets: the whole environment would pass the
		// result cap and hide a leak behind it.
		var out []string
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "WIKI_") || strings.HasPrefix(kv, "CONNECTOR_TEST_") {
				out = append(out, kv)
			}
		}
		return text(strings.Join(out, "\n")), nil, nil
	})
	srv.AddTool(&mcp.Tool{Name: "big", Description: "oversize schema", InputSchema: map[string]any{"type": "object",
		"properties": map[string]any{"q": map[string]any{"type": "string", "description": strings.Repeat("x", 9<<10)}}}},
		func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text("big"), nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "bad.name", Description: "a dot"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		return text("dot"), nil, nil
	})
	return srv
}

func connectorInput(t *testing.T, name string, tools []string, sc *Scenario) ConnectorInput {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sc.Env["CONNECTOR_TEST_HELPER"] = "1"
	return ConnectorInput{Name: "wiki", Scenario: sc, Env: map[string]string{"CONNECTOR_TEST_HELPER": "CONNECTOR_TEST_HELPER_VAR", "WIKI_TOKEN": "CONNECTOR_TEST_TOKEN_VAR"},
		Config: connector.Config{Name: name, Transport: connector.TransportStdio, Command: exe, Tools: tools,
			Env:         map[string]string{"CONNECTOR_TEST_HELPER": "CONNECTOR_TEST_HELPER_VAR", "WIKI_TOKEN": "CONNECTOR_TEST_TOKEN_VAR"},
			CallTimeout: 20 * time.Second, MaxResultBytes: 1 << 10}}
}

func calls(cs ...Call) *Scenario { return &Scenario{Env: map[string]string{}, Calls: cs} }

// A connector that does what its descriptor says passes: the offered tools are
// the allowlist, a call outside it is refused, and the report has one line per call.
func TestAConformingConnectorPasses(t *testing.T) {
	sc := calls(Call{Tool: "read", Args: map[string]any{"id": "7"}, ExpectTextContains: "page 7"}, Call{Tool: "write", ExpectError: true})
	res := RunConnector(context.Background(), connectorInput(t, "acme/wiki", []string{"read"}, sc))
	if len(res) != 3 {
		t.Fatalf("%+v", res)
	}
	for _, r := range res {
		if r.Status != Pass {
			t.Errorf("%+v", r)
		}
	}
}

func TestEveryConnectorProblemIsNamed(t *testing.T) {
	for name, tc := range map[string]struct {
		in    func(t *testing.T) ConnectorInput
		check string // the row that must fail
		want  string
	}{
		"absent tool": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read", "absent"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}}))
		}, "connector", "tool absent is in the allowlist but is not offered: the server does not offer it"},
		"name over the cap": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/"+strings.Repeat("w", 55), []string{"read"}, calls(Call{Tool: "read"}))
		}, "connector", "tool read is in the allowlist but is not offered"},
		"wrong text": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}, ExpectTextContains: "page 2"}))
		}, "call 1 read", "does not contain"},
		"an allowed tool that must fail": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}, ExpectError: true}))
		}, "call 1 read", "expected true"},
		"a secret in a result": {func(t *testing.T) ConnectorInput {
			sc := calls(Call{Tool: "env"})
			sc.Env["WIKI_TOKEN"] = "tok-secret-value"
			return connectorInput(t, "acme/wiki", []string{"env"}, sc)
		}, "connector", "the value of WIKI_TOKEN appears in a call result"},
		"schema over the cap": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read", "big"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}}))
		}, "connector", "tool big is in the allowlist but is not offered: surveyor: connector acme/wiki: tool big not offered: its input schema is over"},
		"a name the providers refuse": {func(t *testing.T) ConnectorInput {
			return connectorInput(t, "acme/wiki", []string{"read", "bad.name"}, calls(Call{Tool: "read", Args: map[string]any{"id": "1"}}))
		}, "connector", `the offered tool name "acme--wiki__bad.name" is not valid`},
		"a secret in a start error": {func(t *testing.T) ConnectorInput {
			sc := calls(Call{Tool: "read"})
			sc.Env["WIKI_TOKEN"] = "tok-secret-value"
			in := connectorInput(t, "acme/wiki", []string{"read"}, sc)
			in.Config.Command = os.Args[0] + "-missing-tok-secret-value"
			return in
		}, "connector", "the value of WIKI_TOKEN appears in"},
		"does not start": {func(t *testing.T) ConnectorInput {
			in := connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read"}))
			in.Config.Command = os.Args[0] + "-missing"
			return in
		}, "connector", "cannot start"},
	} {
		t.Run(name, func(t *testing.T) {
			var failed Result
			for _, r := range RunConnector(context.Background(), tc.in(t)) {
				if r.Status == Fail && r.Check == tc.check {
					failed = r
				}
			}
			if failed.Status != Fail || !strings.Contains(failed.Detail, tc.want) {
				t.Errorf("%q: %+v", tc.check, failed)
			}
		})
	}
}

// The child sees the variables the descriptor lists and nothing else: a variable
// of the parent is not among them (the env tool prints what the child has).
func TestTheChildSeesOnlyTheListedVariables(t *testing.T) {
	t.Setenv("CONNECTOR_TEST_PARENT_ONLY", "parent-only-value-9")
	sc := calls(Call{Tool: "env", ExpectTextContains: "WIKI_TOKEN="})
	sc.Env["PARENT_ONLY"] = "parent-only-value-9" // watched: the verdict names it if the child printed it
	in := connectorInput(t, "acme/wiki", []string{"env"}, sc)
	in.Config.MaxResultBytes = 64 << 10 // room for anything the child prints
	res := RunConnector(context.Background(), in)
	for _, r := range res {
		if r.Check == "call 1 env" && r.Status != Pass {
			t.Errorf("the listed variable is not in the child: %+v", r)
		}
		if r.Check == "connector" && (strings.Contains(r.Detail, "PARENT_ONLY") || !strings.Contains(r.Detail, "WIKI_TOKEN")) {
			t.Errorf("the verdict should name the listed WIKI_TOKEN only: %+v", r)
		}
	}
}

// An http connector is skipped, but its calls are still held to the allowlist: a
// scenario that calls a tool the descriptor does not list is wrong whatever the server is.
func TestAnHTTPConnectorIsSkippedButItsCallsAreChecked(t *testing.T) {
	in := connectorInput(t, "acme/wiki", []string{"read"}, calls(Call{Tool: "read"}, Call{Tool: "write"}, Call{Tool: "write", ExpectError: true}))
	in.Config.Transport = connector.TransportHTTP
	res := RunConnector(context.Background(), in)
	if len(res) != 4 || res[0].Status != Skip || !strings.Contains(res[0].Detail, "live server") {
		t.Fatalf("%+v", res)
	}
	if res[1].Status != Skip || res[2].Status != Fail || !strings.Contains(res[2].Detail, "allowlist") || res[3].Status != Skip {
		t.Errorf("%+v", res)
	}
}
