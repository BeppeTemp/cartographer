package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D746: the KB's settings.yaml findings attach to the file, like paths.yaml's.

func TestRun_SettingsFile(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	if got := findingsOf(mustRun(t, k), "contract_malformed"); len(got) != 0 {
		t.Fatalf("a KB without settings.yaml has findings: %+v", got)
	}

	writeFile(t, k.Root, "settings.yaml", "write_gate: error\nauto_repair: [duplicate_link]\n")
	if got := findingsOf(mustRun(t, k), "contract_malformed"); len(got) != 0 {
		t.Fatalf("a valid file has findings: %+v", got)
	}

	writeFile(t, k.Root, "settings.yaml", "write_gate: loud\nfrobnicate: 1\nremote: x\nauto_repair: [no_such_check]\n")
	got := findingsOf(mustRun(t, k), "contract_malformed")
	if len(got) != 4 {
		t.Fatalf("contract_malformed = %+v, want one per bad entry", got)
	}
	for _, f := range got {
		if f.Path != "settings.yaml" {
			t.Errorf("finding on %q, want settings.yaml", f.Path)
		}
	}
	all := ""
	for _, f := range got {
		all += f.Message + "\n"
	}
	for _, frag := range []string{"write_gate", "unknown setting", "server-only", "no_such_check"} {
		if !strings.Contains(all, frag) {
			t.Errorf("findings do not mention %q:\n%s", frag, all)
		}
	}

	writeFile(t, k.Root, "settings.yaml", "- not a mapping\n")
	if got := findingsOf(mustRun(t, k), "contract_malformed"); len(got) != 1 {
		t.Errorf("a file that is not a mapping: %+v, want one finding", got)
	}
}

func mustRun(t *testing.T, k *kb.KB) []Finding {
	t.Helper()
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}
