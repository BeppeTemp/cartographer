package clientconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// liveTransversal matches a transversal key that is data, not a schema comment.
var liveTransversal = regexp.MustCompile(`(?m)^ +transversal:`)

const legacyTransversalYAML = "server_url: http://127.0.0.1:39273/mcp\nkb_options:\n  kb-a:\n    transversal: true\n    access: read\n"

// AC-transv-3: the realignment moves kb_options into knowledge_bases without the
// flag, and says so; config shows the same.
func TestRealignDropsLegacyTransversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(legacyTransversalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := string(Grouped([]byte(legacyTransversalYAML))); liveTransversal.MatchString(g) || !strings.Contains(g, "access: read") {
		t.Errorf("Grouped (what `config` shows) carries the flag or lost the rest:\n%s", g)
	}
	rep, _, err := RealignFile(dir, "0.30.0")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range rep.Lines() {
		lines = append(lines, l)
	}
	if all := strings.Join(lines, "\n"); !strings.Contains(all, "removed kb_options.kb-a.transversal: the flag exists only under knowledge_bases.kb-a.transversal") {
		t.Errorf("the realign report does not say it:\n%s", all)
	}
	raw, _ := os.ReadFile(Path(dir))
	if liveTransversal.MatchString(string(raw)) {
		t.Errorf("the realigned file carries the flag:\n%s", raw)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KBTransversal("kb-a") || cfg.KBAccess("kb-a") != KBAccessRead {
		t.Errorf("after the realignment: transversal %v, access %s", cfg.KBTransversal("kb-a"), cfg.KBAccess("kb-a"))
	}
}

func TestWarnLegacyTransversalOnce(t *testing.T) {
	w := legacyTransversalWarnings([]byte(legacyTransversalYAML))
	if len(w) != 1 || !strings.Contains(w[0], "kb_options.kb-a.transversal is ignored") {
		t.Fatalf("warnings = %v", w)
	}
	r, wr, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = wr
	path := filepath.Join(t.TempDir(), "x.yaml")
	warnLegacyOnce(path, w)
	warnLegacyOnce(path, w)
	wr.Close()
	os.Stderr = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if got := strings.Count(string(buf[:n]), "kb_options.kb-a.transversal is ignored"); got != 1 {
		t.Errorf("printed %d times, want once:\n%s", got, buf[:n])
	}
}
