package secretscan

import (
	"strings"
	"testing"
)

// Test tokens are fake but format-valid and are ALWAYS assembled at runtime
// from parts: a literal token in a source file trips GitHub push protection and
// the privacy net (D259, D1202). The per-format table lives in internal/lint.

func rep(s string, n int) string { return strings.Repeat(s, n) }

func TestFind_SpanIsTheToken(t *testing.T) {
	t.Parallel()
	toks := map[string]string{
		"GitHub token":      "ghp_" + rep("a1B2", 9),
		"AWS access key id": "AKIA" + rep("AB23", 4),
		"Stripe key":        "sk_live_" + rep("aB3d", 6),
	}
	for kind, tok := range toks {
		text := "intro\nkey: " + tok + " and more\n"
		ms := Find(text)
		if len(ms) != 1 || ms[0].Kind != kind || ms[0].Line != 2 {
			t.Fatalf("%s: %+v", kind, ms)
		}
		m := ms[0]
		if m.Start < 0 || m.End > len(text) || m.Start >= m.End || text[m.Start:m.End] != tok {
			t.Errorf("%s: span %d..%d is %q", kind, m.Start, m.End, text[m.Start:m.End])
		}
	}
}

func TestFind_PEMSpan(t *testing.T) {
	t.Parallel()
	key := rep("MIIEowIBAAKCAQEA", 2)
	hdr := "-----BEGIN RSA PRIVATE KEY-----"
	for name, c := range map[string]struct{ text, want string }{
		"with END":    {"before\n" + hdr + "\n" + key + "\n" + key + "\n-----END RSA PRIVATE KEY-----\nafter\n", hdr + "\n" + key + "\n" + key + "\n-----END RSA PRIVATE KEY-----"},
		"without END": {hdr + "\n" + key + "\ntrailing prose\n", hdr + "\n" + key},
		"encrypted":   {hdr + "\nProc-Type: 4,ENCRYPTED\n\n" + key + "\n", hdr + "\nProc-Type: 4,ENCRYPTED\n\n" + key},
	} {
		ms := Find(c.text)
		if len(ms) != 1 || ms[0].Kind != "PEM private key" || c.text[ms[0].Start:ms[0].End] != c.want {
			t.Errorf("%s: %+v", name, ms)
		}
	}
	if ms := Find(hdr + "\n" + rep("A", 32) + "\n"); len(ms) != 0 {
		t.Errorf("placeholder key matched: %+v", ms)
	}
}
