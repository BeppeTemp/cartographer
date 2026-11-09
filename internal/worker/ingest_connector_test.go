package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/connector"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recModel struct {
	turns []Response
	reqs  []Request
}

func (m *recModel) Complete(_ context.Context, r Request) (Response, error) {
	m.reqs = append(m.reqs, r)
	if len(m.reqs) > len(m.turns) {
		return Response{Text: "done", Stop: StopEnd}, nil
	}
	return m.turns[len(m.reqs)-1], nil
}

type wikiArgs struct {
	ID string `json:"id"`
}

// wikiServer is a third-party MCP server with a read tool and a write tool.
func wikiServer(reads, writes *atomic.Int32) *httptest.Server {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "wiki", Version: "1"}, nil)
		mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: "reads a page"}, func(_ context.Context, _ *mcp.CallToolRequest, in wikiArgs) (*mcp.CallToolResult, any, error) {
			reads.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "page " + in.ID}}}, nil, nil
		})
		mcp.AddTool(srv, &mcp.Tool{Name: "write", Description: "mutates"}, func(_ context.Context, _ *mcp.CallToolRequest, _ wikiArgs) (*mcp.CallToolResult, any, error) {
			writes.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "written"}}}, nil, nil
		})
		return srv
	}, nil)
	return httptest.NewServer(h)
}

func wikiConfig(url string) connector.Config {
	return connector.Config{Name: "wiki", Transport: connector.TransportHTTP, URL: url, AllowInsecureHTTP: true,
		Tools: []string{"read"}, CallTimeout: 20 * time.Second, MaxResultBytes: 1 << 10}
}

func connectorInst(t *testing.T, cfgs ...connector.Config) *ingestInstance {
	in := newInst(t, TierSmall)
	in.connectors = cfgs
	return in
}

func TestIngestConnectorToolsReachOnlyTheClaimedUnitAndOnlyTheAllowlisted(t *testing.T) {
	var reads, writes atomic.Int32
	srv := wikiServer(&reads, &writes)
	defer srv.Close()
	k := newIKB()
	k.src("s/wiki", map[string]any{"connector": "wiki", "locator": "wiki:page/7"})
	k.unit("u/1", "s/wiki", "open", "any", nil)
	in := connectorInst(t, wikiConfig(srv.URL))
	if it := next(t, in, k); it == nil || it.Key != "u/1" {
		t.Fatalf("a unit whose Source names a declared connector was not offered: %+v", it)
	}
	args := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	m := &recModel{turns: []Response{
		{Stop: StopToolUse, Usage: Usage{InputTokens: 100, OutputTokens: 10}, ToolCalls: []ToolCall{
			{ID: "1", Name: "wiki__read", Arguments: args(map[string]any{"id": "7"})},
			{ID: "2", Name: "wiki__write", Arguments: args(map[string]any{})}, // not on the allowlist
			{ID: "3", Name: "concept_write", Arguments: args(map[string]any{"id": "kb/p", "body": "text", "reason": "unit"})},
		}},
		{Stop: StopEnd, Text: "done", Usage: Usage{InputTokens: 100, OutputTokens: 10}},
	}}
	res, err := in.Run(context.Background(), next(t, in, k), rtFor(k, m, "r1"))
	if err != nil || res.Units != 1 || k.status("u/1") != "done" {
		t.Fatalf("run: %+v %v status %s", res, err, k.status("u/1"))
	}
	if reads.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("server saw %d reads and %d writes", reads.Load(), writes.Load())
	}
	var offered []string
	for _, tl := range m.reqs[0].Tools {
		offered = append(offered, tl.Name)
	}
	joined := " " + strings.Join(offered, " ") + " "
	if !strings.Contains(joined, " wiki__read ") || strings.Contains(joined, "wiki__write") {
		t.Fatalf("offered tools: %v", offered)
	}
}

func TestIngestForgedConnectorCallReachesNoServer(t *testing.T) {
	var reads, writes atomic.Int32
	srv := wikiServer(&reads, &writes)
	defer srv.Close()
	set := connector.Open(context.Background(), []connector.Config{wikiConfig(srv.URL)}, nil)
	defer set.Close()
	ts, err := set.Tools(context.Background(), "wiki")
	if err != nil {
		t.Fatal(err)
	}
	tools := &ingestTools{inner: newIKB(), ctx: context.Background(), conns: set, connTools: ts}
	for _, name := range []string{"wiki__write", "other__read", "wiki__"} {
		if out, _ := tools.CallTool(name, map[string]any{}); !out.IsError {
			t.Errorf("%s: %+v", name, out)
		}
	}
	// Without a connector at all, a name with the separator is refused too.
	bare := &ingestTools{inner: newIKB(), ctx: context.Background()}
	if out, _ := bare.CallTool("wiki__read", map[string]any{}); !out.IsError {
		t.Errorf("no connector: %+v", out)
	}
	if reads.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("a forged call reached the server: %d reads, %d writes", reads.Load(), writes.Load())
	}
}

func TestIngestUnitWhoseConnectorCannotStartIsSkippedNotClaimed(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	k := newIKB()
	k.src("s/wiki", map[string]any{"connector": "wiki", "locator": "wiki:page/7"})
	k.unit("u/1", "s/wiki", "open", "any", nil)
	in := connectorInst(t, wikiConfig(url))
	res, err := in.Run(context.Background(), next(t, in, k), rtFor(k, &recModel{}, "r1"))
	if err != nil || !strings.Contains(res.Summary, "connector wiki unavailable") || k.status("u/1") != "open" || len(k.patches) != 0 {
		t.Fatalf("%+v %v status %s patches %v", res, err, k.status("u/1"), k.patches)
	}
	if it := next(t, in, k); it != nil {
		t.Fatalf("the unit is offered again in this run: %+v", it)
	}
}

// Connector tools are offered to the unit-execution task alone: no other task
// builds its tool set with a connector. The tool-set builder is ingestTools.
func TestOnlyTheIngestTaskImportsTheConnectorSet(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	allowed := map[string]bool{"ingest.go": true, "ingest_fetch.go": true, "poll.go": true, "config.go": true, "config_connectors.go": true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, p := range importsOf(t, f) {
			if p == modPath+"internal/worker/connector" && !allowed[f] {
				t.Errorf("%s imports the connector package: connector tools reach the unit-execution task only", f)
			}
		}
	}
	// poll.go uses it for RedactEnv alone, never Open.
	src, err := os.ReadFile("poll.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "connector.Open") {
		t.Error("poll.go opens a connector")
	}
}
