package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "posttooluse", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runHook(t *testing.T, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	oldIn, oldErr := hookStdin, hookStderr
	defer func() { hookStdin, hookStderr = oldIn, oldErr }()
	var errBuf bytes.Buffer
	hookStdin, hookStderr = strings.NewReader(stdin), &errBuf
	// Capture the real stdout: the hook must never write to it.
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	code = cmdHook([]string{"write-findings"})
	w.Close()
	os.Stdout = oldOut
	var outBuf bytes.Buffer
	outBuf.ReadFrom(r)
	return code, outBuf.String(), errBuf.String()
}

func TestWriteFindingsHook_Fixtures(t *testing.T) {
	cases := []struct {
		file      string
		wantCode  int
		wantLines []string
	}{
		{"content_blocks.json", 2, []string{"2 finding(s)", "broken_link kb-a/page: links to kb-a/missing", "orphan kb-a/page:"}},
		{"result_object.json", 2, []string{"1 finding(s)", "index_incomplete"}},
		{"findings_object.json", 2, []string{"1 finding(s)", "orphan"}},
		{"batch.json", 2, []string{"1 finding(s)", "kb-a/two"}},
		{"supersede_text.json", 2, []string{"1 finding(s)", "link_to_retired kb-a/old"}},
		{"clean.json", 0, nil},
		{"gate_refusal.json", 0, nil},
		{"other_tool.json", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			code, stdout, stderr := runHook(t, fixture(t, tc.file))
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tc.wantCode, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout must stay empty, got %q", stdout)
			}
			if tc.wantCode == 0 && stderr != "" {
				t.Errorf("silent case wrote %q", stderr)
			}
			for _, l := range tc.wantLines {
				if !strings.Contains(stderr, l) {
					t.Errorf("stderr lacks %q:\n%s", l, stderr)
				}
			}
			if tc.wantCode == 2 && !strings.Contains(stderr, "lint_ignore") {
				t.Errorf("stderr does not say how to dismiss:\n%s", stderr)
			}
		})
	}
}

func TestWriteFindingsHook_CapsAt10(t *testing.T) {
	var fs []string
	for i := 0; i < 25; i++ {
		fs = append(fs, fmt.Sprintf(`{"path":"kb-a/p%d","check":"orphan","severity":"info","message":"m"}`, i))
	}
	in := `{"tool_name":"mcp__s__concept_batch","tool_response":{"findings":[` + strings.Join(fs, ",") + `]}}`
	code, _, stderr := runHook(t, in)
	if code != 2 || !strings.Contains(stderr, "25 finding(s)") || !strings.Contains(stderr, "+15 more") {
		t.Fatalf("code %d:\n%s", code, stderr)
	}
	if got := strings.Count(stderr, "orphan kb-a/"); got != 10 {
		t.Errorf("%d finding lines, want 10", got)
	}
}

func TestWriteFindingsHook_SilentOnGarbage(t *testing.T) {
	for _, in := range []string{"", "not json", "{}", `{"tool_response":42}`, `{"tool_response":"plain text"}`, `[1,2]`, `{"tool_response":{"findings":"x"}}`} {
		code, stdout, stderr := runHook(t, in)
		if code != 0 || stdout != "" || stderr != "" {
			t.Errorf("input %q: code %d stdout %q stderr %q", in, code, stdout, stderr)
		}
	}
}

func TestWriteFindingsHook_UsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"write-findings", "x"}} {
		if got := cmdHook(args); got != 2 {
			t.Errorf("args %v: exit %d, want 2", args, got)
		}
	}
	if got := run([]string{"hook"}); got != 2 {
		t.Errorf("run hook = %d", got)
	}
}
