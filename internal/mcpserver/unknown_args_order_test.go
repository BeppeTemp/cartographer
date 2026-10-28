package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sdkCall runs one tools/call through the SDK tool handler, the path every
// real client takes, and returns the protocol error it may answer with.
func sdkCall(t *testing.T, s *Server, ctx context.Context, name, args string) (ToolResult, error) {
	t.Helper()
	h := s.sdkToolHandler(name)
	res, err := h(ctx, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(args)}})
	if err != nil {
		return ToolResult{}, err
	}
	var out ToolResult
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: tc.Text})
		}
	}
	out.IsError = res.IsError
	return out, nil
}

// #1279: a declared-hidden alias (map_create's `map`, #836) is accepted on a
// real tools/call, not only when the handler is called directly.
func TestMCPHiddenArgumentIsAcceptedOverHTTP(t *testing.T) {
	t.Parallel()
	h := openHandler(newEraServer(t))
	rr := postMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"map":"alias-map","title":"T"}}}`, nil)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), `"error":{`) || strings.Contains(rr.Body.String(), `"isError":true`) {
		t.Fatalf("map_create with the hidden alias `map` refused: %d %.300s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alias-map") {
		t.Fatalf("map not created: %.300s", rr.Body.String())
	}
}

// The same through the SDK handler, and the removed search arguments keep
// their own message (D135) instead of a schema refusal.
func TestSDKHandlerAcceptsHiddenArguments(t *testing.T) {
	t.Parallel()
	s := newEraServer(t)
	res, err := sdkCall(t, s, authLocalContext(), "map_create", `{"map":"alias-two","title":"T"}`)
	if err != nil || res.IsError {
		t.Fatalf("map alias: err=%v res=%+v", err, res)
	}
	res, err = sdkCall(t, s, authLocalContext(), "search", `{"query":"x","use_semantic":true}`)
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "removed") {
		t.Fatalf("search use_semantic: err=%v res=%+v", err, res)
	}
}

// The check comes after authorization: a caller that may not use a tool is
// told so, whatever else is wrong with its arguments, and cannot probe the
// schema through the refusal.
func TestUnknownArgumentDoesNotMaskAuthorizationDenial(t *testing.T) {
	t.Parallel()
	s := newEraServer(t)
	ro := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: s.PolicyKB()}}}})
	res, err := sdkCall(t, s, ro, "map_create", `{"name":"m","title":"T","bogus_key":1}`)
	if err != nil {
		t.Fatalf("a denied caller got a protocol error: %v", err)
	}
	if !res.IsError || strings.Contains(res.Content[0].Text, "bogus_key") {
		t.Fatalf("want the authorization denial, got %+v", res)
	}
}

// An authorized call with an unknown key is -32602 and is audited as a denial.
func TestUnknownArgumentIsAuditedAsDenial(t *testing.T) {
	t.Parallel()
	s, _, path := auditServer(t, audit.Options{})
	RegisterKBTools(s, setupTestKB(t), Deps{})
	_, err := sdkCall(t, s, authLocalContext(), "concept_write", `{"id":"x/y","if_mach":"abc"}`)
	var je *jsonrpc.Error
	if !errors.As(err, &je) || je.Code != jsonrpc.CodeInvalidParams || !strings.Contains(je.Message, "if_mach") {
		t.Fatalf("err = %v, want -32602 naming if_mach", err)
	}
	var denied int
	for _, e := range auditEntries(t, path) {
		if e.Tool == "concept_write" && e.Outcome == audit.OutcomeUnauthorized {
			denied++
		}
	}
	if denied == 0 {
		t.Fatalf("no denied audit record: %+v", auditEntries(t, path))
	}
}
