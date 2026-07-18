package provisioning

import "testing"

func TestParseMCPServerSpec_Valid(t *testing.T) {
	data := []byte(`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${EXAMPLE_TOKEN}"}}`)
	spec, err := parseMCPServerSpec("example", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != "http" || spec.URL != "https://example.com/mcp" {
		t.Errorf("unexpected spec: %+v", spec)
	}
	if spec.Headers["Authorization"] != "Bearer ${EXAMPLE_TOKEN}" {
		t.Errorf("unexpected headers: %+v", spec.Headers)
	}
}

func TestParseMCPServerSpec_RejectsLiteralSecret(t *testing.T) {
	data := []byte(`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer sk-live-abc123"}}`)
	_, err := parseMCPServerSpec("example", data)
	if err == nil {
		t.Fatal("expected error for a literal (non-${VAR}) header value")
	}
}

func TestParseMCPServerSpec_RejectsLiteralEnvSecret(t *testing.T) {
	data := []byte(`{"type":"http","url":"https://example.com/mcp","env":{"API_KEY":"hardcoded-secret"}}`)
	_, err := parseMCPServerSpec("example", data)
	if err == nil {
		t.Fatal("expected error for a literal (non-${VAR}) env value")
	}
}

func TestParseMCPServerSpec_AcceptsEnvRef(t *testing.T) {
	data := []byte(`{"type":"http","url":"https://example.com/mcp","env":{"API_KEY":"${EXAMPLE_KEY}"}}`)
	if _, err := parseMCPServerSpec("example", data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseMCPServerSpec_RejectsUnsupportedType(t *testing.T) {
	data := []byte(`{"type":"stdio","command":"foo"}`)
	_, err := parseMCPServerSpec("example", data)
	if err == nil {
		t.Fatal("expected error for an unsupported type (only \"http\" in this iteration)")
	}
}

func TestParseMCPServerSpec_RejectsMissingURL(t *testing.T) {
	data := []byte(`{"type":"http"}`)
	if _, err := parseMCPServerSpec("example", data); err == nil {
		t.Fatal("expected error for a missing url")
	}
}

func TestParseMCPServerSpec_RejectsMalformedJSON(t *testing.T) {
	data := []byte(`{not json`)
	if _, err := parseMCPServerSpec("example", data); err == nil {
		t.Fatal("expected error for malformed json")
	}
}
