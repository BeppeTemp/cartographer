package provisioning

import (
	"strings"
	"testing"
)

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

func TestParseMCPServerSpec_StdioValidation(t *testing.T) {
	valid, err := parseMCPServerSpec("example", []byte(`{"type":"stdio","command":"tool","args":["serve","--flag=$HOME"],"env":{"TOKEN":"${TOKEN}","CHAIN":"${ONE}:${TWO}"}}`))
	if err != nil {
		t.Fatalf("valid stdio spec: %v", err)
	}
	if valid.Command != "tool" || len(valid.Args) != 2 || valid.Args[1] != "--flag=$HOME" {
		t.Fatalf("stdio spec lost command or argument order: %+v", valid)
	}

	for name, data := range map[string]string{
		"http-command":  `{"type":"http","url":"https://example.com/mcp","command":"tool"}`,
		"http-relative": `{"type":"http","url":"/mcp"}`,
		"stdio-url":     `{"type":"stdio","command":"tool","url":"https://example.com/mcp"}`,
		"stdio-headers": `{"type":"stdio","command":"tool","headers":{"X":"${TOKEN}"}}`,
		"empty-command": `{"type":"stdio","command":" "}`,
		"relative-path": `{"type":"stdio","command":"bin/tool"}`,
		"shell-command": `{"type":"stdio","command":"tool;echo"}`,
		"literal-env":   `{"type":"stdio","command":"tool","env":{"TOKEN":"literal"}}`,
		"bad-env-name":  `{"type":"stdio","command":"tool","env":{"TOKEN-NAME":"${TOKEN}"}}`,
		"unknown-field": `{"type":"stdio","command":"tool","unknown":true}`,
		"unknown-type":  `{"type":"socket","command":"tool"}`,
		"two-values":    `{"type":"stdio","command":"tool"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMCPServerSpec("example", []byte(data)); err == nil {
				t.Fatalf("accepted invalid descriptor: %s", data)
			}
		})
	}
}

func TestParseMCPServerSpec_CommandPaths(t *testing.T) {
	for _, command := range []string{"tool", "/usr/local/bin/tool"} {
		if _, err := parseMCPServerSpec("example", []byte(`{"type":"stdio","command":"`+command+`"}`)); err != nil {
			t.Errorf("command %q: %v", command, err)
		}
	}
	if _, err := parseMCPServerSpec("example", []byte(`{"type":"stdio","command":"/usr/local/../bin/tool"}`)); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Errorf("unclean absolute path error = %v", err)
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
