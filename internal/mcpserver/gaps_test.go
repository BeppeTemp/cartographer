package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// D273: gaps are Contradiction concepts with a reserved kind. kb_status counts
// them apart; contradiction_report can filter on them.
func TestKBStatus_OpenGapsSplitFromContradictions(t *testing.T) {
	k, s, _ := missFixture(t)
	status := func() map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, ok := status()["open_gaps"]; ok {
		t.Fatal("open_gaps present with no gaps")
	}

	write := func(name, kind, extra, status string) {
		t.Helper()
		body := "---\ntype: Contradiction\ntitle: " + name + "\nresolution_status: " + status + "\n"
		if kind != "" {
			body += "contradiction_kind: " + kind + "\n"
		}
		body += extra + "---\nBody.\n"
		writeTestFile(t, k.DataRoot(), "gaps/"+name+".md", body)
	}
	write("clash", "conflict", "involves: [a/b, a/c]\n", "open")
	write("nokind", "", "", "open")
	write("q-old", "open_question", "timestamp: 2026-01-01T00:00:00Z\n", "open")
	write("m-new", "missing_context", "timestamp: 2026-05-01T00:00:00Z\ninvolves: [a/b]\n", "open")
	write("q-done", "open_question", "", "resolved")
	for i := 0; i < 11; i++ {
		write(fmt.Sprintf("q-%02d", i), "open_question", "", "open")
	}

	m := status()
	var oc int
	if err := json.Unmarshal(m["open_contradictions"], &oc); err != nil || oc != 2 {
		t.Fatalf("open_contradictions = %s (%v), want 2", m["open_contradictions"], err)
	}
	var gaps struct {
		Total  int            `json:"total"`
		ByKind map[string]int `json:"by_kind"`
		Recent []struct {
			ID       string   `json:"id"`
			Kind     string   `json:"kind"`
			Involves []string `json:"involves"`
		} `json:"recent"`
	}
	if err := json.Unmarshal(m["open_gaps"], &gaps); err != nil {
		t.Fatal(err)
	}
	if gaps.Total != 13 || gaps.ByKind["open_question"] != 12 || gaps.ByKind["missing_context"] != 1 {
		t.Fatalf("gaps = %+v", gaps)
	}
	if len(gaps.Recent) != 10 {
		t.Fatalf("recent = %d, want 10", len(gaps.Recent))
	}
	if gaps.Recent[0].ID != "gaps/m-new" || len(gaps.Recent[0].Involves) != 1 {
		t.Fatalf("newest = %+v", gaps.Recent[0])
	}
	if gaps.Recent[1].ID != "gaps/q-old" {
		t.Fatalf("second = %+v, want the older timestamp before untimestamped", gaps.Recent[1])
	}
}

func TestContradictionReport_KindFilter(t *testing.T) {
	k, s, _ := missFixture(t)
	for name, kind := range map[string]string{"clash": "conflict", "nokind": "", "q": "open_question", "m": "missing_context"} {
		body := "---\ntype: Contradiction\nresolution_status: open\n"
		if kind != "" {
			body += "contradiction_kind: " + kind + "\n"
		}
		writeTestFile(t, k.DataRoot(), "gaps/"+name+".md", body+"---\nBody.\n")
	}
	ids := func(kind string) string {
		var got []string
		for _, line := range strings.Split(callOK(t, s, "contradiction_report", `{"kind":"`+kind+`"}`), "\n") {
			if f := strings.Fields(line); len(f) > 1 {
				got = append(got, strings.TrimPrefix(f[1], "gaps/"))
			}
		}
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	for kind, want := range map[string]string{
		"gap":           "m,q",
		"contradiction": "clash,nokind",
		"open_question": "q",
		"":              "clash,m,nokind,q",
	} {
		if got := ids(kind); got != want {
			t.Errorf("kind %q: got %q want %q", kind, got, want)
		}
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
