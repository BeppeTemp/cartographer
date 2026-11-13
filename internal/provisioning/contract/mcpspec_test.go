package contract

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

func TestParseMCPServerSpec_SecretRefsAndEnvLiteral(t *testing.T) {
	// A GitHub-token-shaped value, built at runtime so the file holds no token (D1202).
	token := "gh" + "p_" + strings.Repeat("a1B2", 9)
	const stdio = `{"type":"stdio","command":"srv",`
	cases := []struct {
		name, json, wantErr string
	}{
		{"secret in env", stdio + `"env":{"T":"${secret:services/svc#SVC_TOKEN}"}}`, ""},
		{"secret in a longer header", `{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${secret:services/svc#TOKEN}"}}`, ""},
		{"secret in url", `{"type":"http","url":"https://example.com/${secret:svc#TOKEN}","headers":{"A":"${X}"}}`, "not in url"},
		{"secret in args", stdio + `"args":["--k=${secret:svc#TOKEN}"],"env":{"T":"${A}"}}`, "not in args"},
		{"literal accepted", stdio + `"env_literal":{"NODE_OPTIONS":"--use-system-ca"}}`, ""},
		{"empty literal accepted", stdio + `"env_literal":{"X":""}}`, ""},
		{"no hash", stdio + `"env":{"T":"${secret:services/svc}"}}`, "malformed secret reference"},
		{"dot dot", stdio + `"env":{"T":"${secret:services/../svc#TOKEN}"}}`, "malformed secret reference"},
		{"leading slash", stdio + `"env":{"T":"${secret:/svc#TOKEN}"}}`, "malformed secret reference"},
		{"empty segment", stdio + `"env":{"T":"${secret:a//b#TOKEN}"}}`, "malformed secret reference"},
		{"bad name", stdio + `"env":{"T":"${secret:svc#1X}"}}`, "malformed secret reference"},
		{"unclosed", stdio + `"env":{"T":"${secret:svc#TOKEN"}}`, "malformed secret reference"},
		{"literal text in env", stdio + `"env":{"T":"plain"}}`, "does not reference"},
		{"secret in env_literal", stdio + `"env_literal":{"T":"${secret:svc#TOKEN}"}}`, "${"},
		{"var in env_literal", stdio + `"env_literal":{"T":"${HOME}"}}`, "${"},
		{"newline in env_literal", stdio + `"env_literal":{"T":"a\nb"}}`, "newline"},
		{"duplicate key", stdio + `"env":{"T":"${A}"},"env_literal":{"T":"x"}}`, "both env and env_literal"},
		{"bad key", stdio + `"env_literal":{"A-B":"x"}}`, "portable variable name"},
		{"token literal", stdio + `"env_literal":{"T":"` + token + `"}}`, "looks like a secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseMCPServerSpec("srv", []byte(c.json))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want %q", err, c.wantErr)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("error echoes the value: %v", err)
			}
		})
	}
}

func TestSecretRefs(t *testing.T) {
	refs := SecretRefs("a ${secret:services/svc#A} b ${secret:x#B_1}")
	if len(refs) != 2 || refs[0] != (SecretRef{"services/svc", "A"}) || refs[1] != (SecretRef{"x", "B_1"}) {
		t.Fatalf("refs = %+v", refs)
	}
	if !(MCPServerSpec{Env: map[string]string{"T": "${secret:x#B}"}}).HasSecretRef() {
		t.Error("HasSecretRef = false for an env reference")
	}
	if (MCPServerSpec{Env: map[string]string{"T": "${B}"}}).HasSecretRef() {
		t.Error("HasSecretRef = true for a plain ${VAR}")
	}
}

func TestMCPEnvRefs(t *testing.T) {
	spec := MCPServerSpec{
		Type:    "http",
		URL:     "https://example.com/${NOT_LISTED}",
		Headers: map[string]string{"Authorization": "Bearer ${B_TOKEN}", "X": "${A_KEY}-${B_TOKEN}"},
		Env:     map[string]string{"K": "${C_VAR}", "S": "${secret:svc#KEY}"},
	}
	got := strings.Join(MCPEnvRefs(spec), ",")
	if got != "A_KEY,B_TOKEN,C_VAR" {
		t.Errorf("MCPEnvRefs = %q, want A_KEY,B_TOKEN,C_VAR (sorted, unique, url and secret form excluded)", got)
	}
	if len(MCPEnvRefs(MCPServerSpec{Type: "stdio", Command: "x"})) != 0 {
		t.Error("a descriptor with no reference lists none")
	}
}
