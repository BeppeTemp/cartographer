package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// blockFile writes a managed block with one region per (name, size) into
// <home>/AGENTS.md and a lockfile that records it as written (D1182).
func blockFile(t *testing.T, home string, regions map[string]int, terminate bool) provisioning.LockFile {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("<!-- cartographer:instructions:begin — block managed by Cartographer, do not edit by hand -->\n")
	for name, n := range regions {
		fmt.Fprintf(&sb, "<!-- cartographer:kb:%s:begin -->\n%s\n<!-- cartographer:kb:%s:end -->\n\n", name, strings.Repeat("x", n), name)
	}
	if terminate {
		sb.WriteString("<!-- cartographer:instructions:end -->\n")
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := provisioning.Lock{Provider: "codex", Managed: []provisioning.ManagedFile{{Kind: "instructions", Name: "kb-a", Path: "AGENTS.md"}}}
	return provisioning.LockFile{Providers: map[string]provisioning.Lock{"codex": lock}}
}

func TestInstructionsBlocks_UnderAndOver(t *testing.T) {
	home := t.TempDir()
	lf := blockFile(t, home, map[string]int{"kb-a": 500}, true)
	got := snapshotInstructionsBlocks(lf, "codex", home)
	if len(got) != 1 || got[0].Over || got[0].Threshold != provisioning.InstructionsBlockWarnBytes {
		t.Fatalf("under the threshold: %+v", got)
	}
	out := withStdout(t, func() { printInstructionsSizeLines(providerStatus{Name: "codex", InstructionsBlocks: got}) })
	if out != "" {
		t.Errorf("no line when under: %q", out)
	}

	lf = blockFile(t, home, map[string]int{"kb-a": 9000, "kb-b": 7000, "kb-c": 2000, "kb-d": 1000}, true)
	got = snapshotInstructionsBlocks(lf, "codex", home)
	if len(got) != 1 || !got[0].Over {
		t.Fatalf("over the threshold: %+v", got)
	}
	b := got[0]
	if len(b.Largest) != 3 || b.Largest[0].KB != "kb-a" || b.Largest[1].KB != "kb-b" || b.Largest[2].KB != "kb-c" {
		t.Errorf("largest three, in order: %+v", b.Largest)
	}
	if b.ApproxTokens != (b.Bytes+2)/4 {
		t.Errorf("tokens = %d for %d bytes", b.ApproxTokens, b.Bytes)
	}
	out = withStdout(t, func() { printInstructionsSizeLines(providerStatus{Name: "codex", InstructionsBlocks: got}) })
	for _, want := range []string{"[codex] instructions block", "KiB (~", "tokens, approximate) over 16 KiB", "kb-a 8.", "drop `block: full`"} {
		if !strings.Contains(out, want) {
			t.Errorf("line lacks %q: %q", want, out)
		}
	}

	raw, err := json.Marshal(providerStatus{Name: "codex", InstructionsBlocks: got})
	if err != nil || !strings.Contains(string(raw), `"instructions_blocks"`) || !strings.Contains(string(raw), `"over":true`) {
		t.Errorf("json: %s %v", raw, err)
	}
}

func TestInstructionsBlocks_NothingToMeasure(t *testing.T) {
	home := t.TempDir()
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{"codex": {Managed: []provisioning.ManagedFile{{Kind: "instructions", Name: "kb-a", Path: "AGENTS.md"}}}}}
	if got := snapshotInstructionsBlocks(lf, "codex", home); len(got) != 0 {
		t.Errorf("missing file: %+v", got)
	}
	lf = blockFile(t, home, map[string]int{"kb-a": 20000}, false)
	if got := snapshotInstructionsBlocks(lf, "codex", home); len(got) != 0 {
		t.Errorf("unterminated block: %+v", got)
	}
	if got := snapshotInstructionsBlocks(provisioning.LockFile{}, "codex", home); len(got) != 0 {
		t.Errorf("no lock entry (derived binding): %+v", got)
	}
}

func TestSync_InstructionsSizeWarning(t *testing.T) {
	home := t.TempDir()
	lf := blockFile(t, home, map[string]int{"kb-a": 20000}, true)
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	results := map[string]provisioning.AppliedResult{"codex": {}}
	if out := withStdout(t, func() { printInstructionsSizeWarnings(home, results, false) }); !strings.Contains(out, "[codex] instructions block") {
		t.Errorf("sync must print the line when over: %q", out)
	}
	if out := withStdout(t, func() { printInstructionsSizeWarnings(home, results, true) }); out != "" {
		t.Errorf("a dry run prints nothing: %q", out)
	}
	lf = blockFile(t, home, map[string]int{"kb-a": 500}, true)
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	if out := withStdout(t, func() { printInstructionsSizeWarnings(home, results, false) }); out != "" {
		t.Errorf("no line when under: %q", out)
	}
}
