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

// D216 WP4. The property being fixed is that the verdict is the same on every
// host for the same declared command: a descriptor lives in the KB and is
// validated wherever it is read, so a table that passed here and failed on the
// windows leg would mean the rule is the host's, not the KB's. Nothing in this
// table branches on GOOS, and that is the assertion.
func TestValidateMCPStdioCommand_SameVerdictOnEveryHost(t *testing.T) {
	accepted := []string{
		"srv.exe",
		"tool",
		"/usr/local/bin/tool",
		`C:\srv\srv.exe`,
		"C:/srv/srv.exe",
		// Parentheses, allowed for an absolute command only: this is the most
		// common absolute path on Windows and the whole reason for the
		// relaxation. An absolute command reaches exec.Command with a separate
		// argv and never a shell, so the ban bought nothing.
		`C:\Program Files (x86)\srv\srv.exe`,
		`\\host\share\srv.exe`,
		// A tilde is legal in a file name and appears in every 8.3 short path.
		`C:\Users\RUNNER~1\AppData\srv.exe`,
	}
	rejected := []string{
		"",
		" srv.exe",
		"bin/tool",
		`bin\tool`,
		".",
		"..",
		"srv.exe --flag",
		// Parentheses stay rejected for a bare name: the string is short, has no
		// reason to carry one, and something other than this package decides
		// what it resolves to.
		"srv(x).exe",
		// Every other metacharacter stays rejected, absolute or not.
		"/usr/local/bin/tool;echo",
		"/usr/local/bin/too|l",
		"/usr/local/bin/to$ol",
		"C:\\srv\\srv.exe&whoami",
		"/usr/local/bin/\x00tool",
		// Unclean, in either spelling. The backslash form used to pass: the
		// separator swap in windowsCleanPath was written `\\` in a Go raw string,
		// so it matched a doubled backslash, which no path contains — nothing was
		// normalised and every backslash path was called clean.
		"/usr/local/../bin/tool",
		`C:\srv\..\srv.exe`,
		"C:/srv/../srv.exe",
	}

	for _, command := range accepted {
		if err := ValidateMCPStdioCommand(command); err != nil {
			t.Errorf("ValidateMCPStdioCommand(%q) = %v, want accepted", command, err)
		}
	}
	for _, command := range rejected {
		if err := ValidateMCPStdioCommand(command); err == nil {
			t.Errorf("ValidateMCPStdioCommand(%q) = nil, want rejected", command)
		}
	}
}

// cleanCommandPath must normalise in the syntax the path is written in, on any
// host: this is what the cleanliness check above rests on.
func TestCleanCommandPathIsHostIndependent(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`C:\srv\srv.exe`, `C:\srv\srv.exe`},
		{`C:\srv\..\srv.exe`, `C:\srv.exe`},
		{`\\host\share\..\x`, `\\host\x`},
		{"/usr/bin/tool", "/usr/bin/tool"},
		{"/usr/../bin/tool", "/bin/tool"},
		{"C:/srv/../srv.exe", "C:/srv.exe"},
	} {
		if got := cleanCommandPath(c.in); got != c.want {
			t.Errorf("cleanCommandPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
