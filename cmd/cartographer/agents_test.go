package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// The table is read by a human eye scanning columns, so the property that must
// hold is alignment, not a particular width: a provider name longer than any
// seen today must still leave its own column and a separating space (#303).
func TestWriteAgentsTable_AlignsColumnsWhateverTheProviderNameLength(t *testing.T) {
	detected := []agents.Agent{
		{Provider: configurator.Provider("claude"), Installed: true, Evidence: "/opt/homebrew/bin/claude", DetectedBy: agents.HeuristicBinary},
		{Provider: configurator.Provider("antigravity")},
		{Provider: configurator.Provider("a-very-long-future-provider"), Installed: true, Evidence: "/usr/local/bin/future", DetectedBy: agents.HeuristicEnvConfigDir},
	}
	connected := map[string]bool{"claude": true}

	var buf bytes.Buffer
	writeAgentsTable(&buf, detected, connected)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(detected)+1 {
		t.Fatalf("got %d lines, want %d (header + one per agent):\n%s", len(lines), len(detected)+1, buf.String())
	}

	// Every column of every row, header included, starts at the same offset.
	want := columnOffsets(t, lines[0])
	for _, line := range lines[1:] {
		if got := columnOffsets(t, line); !equalInts(got, want) {
			t.Errorf("column offsets %v of %q differ from the header's %v\n%s", got, line, want, buf.String())
		}
	}

	// The widest provider name still leaves a separating space before INSTALLED.
	for i, line := range lines[1:] {
		name := string(detected[i].Provider)
		if !strings.HasPrefix(line, name+" ") {
			t.Errorf("row %q does not separate the provider name %q from the next column", line, name)
		}
	}
}

// columnOffsets returns the index at which each space-separated column of a
// table row starts.
func columnOffsets(t *testing.T, line string) []int {
	t.Helper()
	var offsets []int
	inColumn := false
	for i, r := range line {
		switch {
		case r != ' ' && !inColumn:
			offsets = append(offsets, i)
			inColumn = true
		case r == ' ':
			inColumn = false
		}
	}
	if len(offsets) != 5 {
		t.Fatalf("row %q has %d columns, want 5", line, len(offsets))
	}
	return offsets
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The evidence must be readable as what it is: a config directory left behind
// by a removed client is reported as a directory, not as an installed binary
// (#305). Not installed means no heuristic at all.
func TestWriteAgentsTable_NamesTheHeuristicNextToTheEvidence(t *testing.T) {
	detected := []agents.Agent{
		{Provider: configurator.Provider("claude"), Installed: true, Evidence: "/home/u/.claude", DetectedBy: agents.HeuristicConfigDir},
		{Provider: configurator.Provider("codex"), Installed: true, Evidence: "/usr/bin/codex", DetectedBy: agents.HeuristicBinary},
		{Provider: configurator.Provider("kiro")},
	}

	var buf bytes.Buffer
	writeAgentsTable(&buf, detected, nil)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[0], "DETECTION") {
		t.Errorf("header %q does not name the detection column", lines[0])
	}
	for i, want := range []string{"config-dir /home/u/.claude", "binary     /usr/bin/codex", "-          -"} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("row %q does not contain %q\n%s", lines[i+1], want, buf.String())
		}
	}
}
