package lint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Test tokens are fake but format-valid and are ALWAYS assembled at runtime
// from parts: a literal token in a source file, fixture, doc or golden trips
// GitHub push protection and the privacy net (D259, D1202). Never paste one.

func rep(s string, n int) string { return strings.Repeat(s, n) }

type secretRow struct {
	name string
	kind string
	pos  string   // a token that must be found
	neg  []string // placeholders and near-misses that must not
}

func secretRows() []secretRow {
	alnum36 := rep("a1B2", 9)
	return []secretRow{
		{"github", "GitHub token", "ghp_" + alnum36, []string{"ghp_" + rep("a", 36), "ghp_" + rep("xxxx", 9), "ghp_" + alnum36[:35], "ghp_" + alnum36 + "Z", "ghp_<token>"}},
		{"github_pat", "GitHub token", "github_pat_" + rep("aB3", 7) + "x" + "_" + rep("c4D", 19) + "ef", []string{"github_pat_" + rep("a", 22) + "_" + rep("a", 59), "github_pat_" + rep("aB3", 7) + "x_short"}},
		{"gitlab", "GitLab token", "glpat-" + rep("aB3_", 6), []string{"glpat-" + rep("a", 20), "glpat-" + rep("aB", 9), "glpat-<token>"}},
		{"anthropic", "Anthropic API key", "sk-ant-api03-" + rep("aB3-_", 17), []string{"sk-ant-api03-" + rep("a", 85), "sk-ant-api03-" + rep("aB3", 5), "sk-ant-…"}},
		{"openai_proj", "OpenAI API key", "sk-proj-" + rep("aB3_", 11), []string{"sk-proj-" + rep("x", 44), "sk-proj-" + rep("aB3", 5)}},
		{"openai_legacy", "OpenAI API key", "sk-" + rep("aB3d", 12), []string{"sk-" + rep("a", 48), "sk-" + rep("aB3d", 11), "sk-" + rep("aB3d", 12) + "Q"}},
		{"aws", "AWS access key id", "AKIA" + rep("AB23", 4), []string{"AKIA" + rep("A", 16), "AKIA" + rep("AB23", 3), "AKIA" + rep("AB18", 4), "AKIA" + rep("X", 16)}},
		{"slack", "Slack token", "xoxb-1234567890-aBcDeF", []string{"xoxb-abcdefghijkl", "xoxb-short", "xoxb-" + rep("0", 12)}},
		{"google", "Google API key", "AIza" + rep("aB3_", 8) + "xyz", []string{"AIza" + rep("a", 35), "AIza" + rep("aB3", 5)}},
		{"stripe", "Stripe key", "sk_live_" + rep("aB3d", 6), []string{"sk_live_" + rep("a", 24), "sk_live_" + rep("aB3", 5), "sk_live_" + rep("xxxx", 6)}},
		{"age", "age secret key", "AGE-SECRET-KEY-1" + rep("QPZRY9X8GF", 5) + "QPZRY9X8", []string{"AGE-SECRET-KEY-1" + rep("Q", 58), "AGE-SECRET-KEY-1" + rep("QPZRY9X8GF", 5), "AGE-SECRET-KEY-1" + rep("B", 58)}},
		{"pem", "PEM private key", "-----BEGIN RSA PRIVATE KEY-----\n" + rep("MIIEowIBAAKCAQEA", 2) + "\n", []string{"-----BEGIN PRIVATE KEY-----\n", "a file starting with -----BEGIN PRIVATE KEY----- is a key", "-----BEGIN PRIVATE KEY-----\n" + rep("A", 32) + "\n", "-----BEGIN PRIVATE KEY-----\n\n-----END PRIVATE KEY-----"}},
	}
}

func kindsOf(ms []secretMatch) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.kind)
	}
	return out
}

func TestSecretMatches_OneRowPerPattern(t *testing.T) {
	t.Parallel()
	for _, r := range secretRows() {
		got := secretMatches("key: " + r.pos + "\n")
		if len(got) != 1 || got[0].kind != r.kind {
			t.Errorf("%s: positive gave %v", r.name, kindsOf(got))
		}
		for _, n := range r.neg {
			if got := secretMatches("key: " + n + "\n"); len(got) != 0 {
				t.Errorf("%s: negative %q matched %v", r.name, n[:min(len(n), 12)], kindsOf(got))
			}
		}
	}
}

func TestSecretMatches_FenceLineAndKinds(t *testing.T) {
	t.Parallel()
	gh := "ghp_" + rep("a1B2", 9)
	got := secretMatches("intro\n```sh\ncurl -H \"Authorization: " + gh + "\"\n```\n")
	if len(got) != 1 || got[0].line != 3 {
		t.Fatalf("fenced token: %v", got)
	}
	if two := secretMatches(gh + " AKIA" + rep("AB23", 4)); len(two) != 2 {
		t.Fatalf("two kinds on one line: %v", two)
	}
	if got := secretMatches(gh + " " + gh); len(got) != 1 {
		t.Fatalf("same kind and line reported twice: %v", got)
	}
	ant := secretMatches("sk-ant-api03-" + rep("aB3-_", 17))
	if len(ant) != 1 || ant[0].kind != "Anthropic API key" {
		t.Fatalf("anthropic also read as legacy OpenAI: %v", ant)
	}
}

func TestSecretMatches_BoundariesAndPEMShapes(t *testing.T) {
	t.Parallel()
	gh := "ghp_" + rep("a1B2", 9)
	for _, text := range []string{"_" + gh + "_", "--token-" + gh, gh + "-prod", "**" + gh + "**"} {
		if got := secretMatches(text); len(got) != 1 {
			t.Errorf("%q not found: %v", text[:6], got)
		}
	}
	if got := secretMatches("x" + gh); len(got) != 0 {
		t.Errorf("alnum-led token matched: %v", got)
	}
	key := rep("MIIEowIBAAKCAQEA", 2)
	for name, text := range map[string]string{
		"encrypted": "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,0123\n\n" + key + "\n",
		"quoted":    "> -----BEGIN PRIVATE KEY-----\n> " + key + "\n",
		"listed":    "- -----BEGIN PRIVATE KEY-----\n  - " + key + "\n",
	} {
		if got := secretMatches(text); len(got) != 1 {
			t.Errorf("%s PEM: %v", name, got)
		}
	}
	if got := secretMatches("> -----BEGIN PRIVATE KEY-----\n> Proc-Type: 4\n"); len(got) != 0 {
		t.Errorf("header only flagged: %v", got)
	}
}

func TestPotentialSecret_NestedBlock(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	page := secretPage("credentials:\n  token: ghp_"+rep("a1B2", 9)+"\n", "clean")
	fs := findingsOf(CheckConcept(k, okf.ConceptID("arch/s"), page), "potential_secret")
	if len(fs) != 1 || !strings.Contains(fs[0].Message, `"credentials"`) {
		t.Fatalf("nested block: %+v", fs)
	}
}

func secretPage(fm, body string) string {
	return "---\ntype: Note\ntitle: S\n" + fm + "---\n# S\n\n" + body + "\n"
}

// The no-echo rule (D1202): no finding, once marshalled, holds the token or
// 8 characters of it past the static prefix.
func TestPotentialSecret_NeverEchoed(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	for _, r := range secretRows() {
		if r.name == "pem" {
			continue
		}
		for _, content := range []string{secretPage("", "x "+r.pos), secretPage("note: "+r.pos+"\n", "clean")} {
			fs := findingsOf(CheckConcept(k, okf.ConceptID("arch/s"), content), "potential_secret")
			if len(fs) != 1 {
				t.Fatalf("%s: %d findings", r.name, len(fs))
			}
			b, _ := json.Marshal(fs)
			out := string(b)
			p := prefixLen(r.pos)
			if strings.Contains(out, r.pos) || strings.Contains(out, r.pos[p:p+8]) || strings.Contains(out, r.pos[len(r.pos)-8:]) {
				t.Errorf("%s: finding echoes the token: %s", r.name, out)
			}
		}
	}
}

func TestPotentialSecret_RunAgreesAndScope(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	root := k.DataRoot()
	tok := "ghp_" + rep("a1B2", 9)
	writeFile(t, root, "arch/body.md", secretPage("", "see "+tok))
	writeFile(t, root, "arch/fm.md", secretPage("note: "+tok+"\n", "clean"))
	writeFile(t, root, "arch/ext.md", secretPage("origin: external\n", "verbatim "+tok))
	writeFile(t, k.Root, "secrets/vault.md", secretPage("", tok)) // beside the data root, as a KB keeps it
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"arch/body", "arch/fm", "arch/ext"} {
		path := okf.IDToPath(okf.ConceptID(id))
		var run, write []string
		for _, f := range findingsOf(findings, "potential_secret") {
			if f.Path == path {
				run = append(run, f.Message)
			}
		}
		cd, _ := k.ReadConcept(okf.ConceptID(id))
		for _, f := range findingsOf(CheckConcept(k, okf.ConceptID(id), cd.Content), "potential_secret") {
			write = append(write, f.Message)
		}
		if len(run) != 1 || len(write) != 1 || run[0] != write[0] {
			t.Errorf("%s: run %q write %q", id, run, write)
		}
	}
	for _, f := range findingsOf(findings, "potential_secret") {
		if strings.HasPrefix(f.Path, "secrets/") {
			t.Errorf("secrets/ artifact reported: %s", f.Path)
		}
	}
	s, _ := Spec("potential_secret")
	if len(s.FixKinds) != 0 || s.AutoRepairSafe || !s.AvoidableAtWrite {
		t.Errorf("spec = %+v", s)
	}
	for _, c := range FixableChecks {
		if c == "potential_secret" {
			t.Fatal("potential_secret must not be accepted by kb_repair")
		}
	}
}
