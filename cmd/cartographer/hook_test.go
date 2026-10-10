package main

import (
	"bytes"
	"encoding/json"
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
	return runHookArgs(t, stdin, "write-findings")
}

func runHookArgs(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	oldIn, oldErr := hookStdin, hookStderr
	defer func() { hookStdin, hookStderr = oldIn, oldErr }()
	var errBuf bytes.Buffer
	hookStdin, hookStderr = strings.NewReader(stdin), &errBuf
	// Capture the real stdout: the hook must never write to it.
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	code = cmdHook(args)
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
	for _, args := range [][]string{nil, {"other"}} {
		if got := cmdHook(args); got != 2 {
			t.Errorf("args %v: exit %d, want 2", args, got)
		}
	}
	if got := run([]string{"hook"}); got != 2 {
		t.Errorf("run hook = %d", got)
	}
}

// codexPayload is the shape the Codex 0.162.0 probe delivered (D361): an MCP
// result object with content blocks, tool named mcp__<server>__<tool>.
const codexPayload = `{"session_id":"s","turn_id":"t","cwd":"/work","model":"m","permission_mode":"default","tool_use_id":"u",` +
	`"tool_name":"mcp__kb__concept_write","tool_input":{"path":"kb-a/page"},` +
	`"tool_response":{"content":[{"type":"text","text":"{\"findings\":[{\"path\":\"kb-a/page\",\"check\":\"broken_link\",\"message\":\"links to kb-a/missing\"}]}"}]}}`

func TestWriteFindingsHook_ContextChannel(t *testing.T) {
	code, stdout, stderr := runHookArgs(t, codexPayload, "write-findings", "--channel", "context")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q, want 0 and silent stderr", code, stderr)
	}
	var out struct {
		H struct {
			Event string `json:"hookEventName"`
			Ctx   string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout, err)
	}
	if out.H.Event != "PostToolUse" || !strings.Contains(out.H.Ctx, "1 finding(s)") || !strings.Contains(out.H.Ctx, "broken_link kb-a/page") {
		t.Errorf("bad output: %+v", out)
	}
}

func TestWriteFindingsHook_StderrChannelOnCodexPayload(t *testing.T) {
	for _, args := range [][]string{{"write-findings"}, {"write-findings", "--channel", "stderr"}} {
		code, stdout, stderr := runHookArgs(t, codexPayload, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "broken_link kb-a/page") {
			t.Errorf("args %v: code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestWriteFindingsHook_ContextChannelSilentWithoutFindings(t *testing.T) {
	for _, in := range []string{fixture(t, "clean.json"), fixture(t, "other_tool.json"), "", "not json"} {
		code, stdout, stderr := runHookArgs(t, in, "write-findings", "--channel", "context")
		if code != 0 || stdout != "" || stderr != "" {
			t.Errorf("input %q: code %d stdout %q stderr %q", in, code, stdout, stderr)
		}
	}
}

// A hook must never break a session: a bad flag is exit 0, not usage exit 2.
func TestWriteFindingsHook_BadFlagNeverReturns2(t *testing.T) {
	for _, args := range [][]string{
		{"write-findings", "x"},
		{"write-findings", "--channel"},
		{"write-findings", "--channel", "nope"},
	} {
		code, stdout, _ := runHookArgs(t, codexPayload, args...)
		if code != 0 || stdout != "" {
			t.Errorf("args %v: code %d stdout %q", args, code, stdout)
		}
	}
}

func TestWriteFindingsHook_ContextChannelRecoversFromPanic(t *testing.T) {
	old := hookStdin
	defer func() { hookStdin = old }()
	hookStdin = panicReader{}
	if got := cmdHook([]string{"write-findings", "--channel", "context"}); got != 0 {
		t.Errorf("panic exit = %d, want 0", got)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

// TestWriteFindingsHook_OpenCodeToolNames: the OpenCode 2.0.25 probe (D362) delivers
// the inner MCP call as <server>_<tool> with the result as a plain string.
func TestWriteFindingsHook_OpenCodeToolNames(t *testing.T) {
	resp := `{\"findings\":[{\"path\":\"kb-a/a\",\"check\":\"broken_link\",\"message\":\"m1\"},{\"path\":\"kb-a/b\",\"check\":\"orphan\",\"message\":\"m2\"}]}`
	code, _, stderr := runHook(t, `{"tool_name":"kb_concept_write","tool_response":"`+resp+`"}`)
	if code != 2 || !strings.Contains(stderr, "2 finding(s)") || !strings.Contains(stderr, "broken_link kb-a/a") {
		t.Fatalf("code %d:\n%s", code, stderr)
	}
	// OpenCode joins the content blocks: the sync-state block follows the JSON.
	joined := `{\"findings\":[{\"path\":\"kb-a/a\",\"check\":\"orphan\",\"message\":\"m\"}]}\n{\"last_error\":\"\",\"sync_state\":\"pending\"}`
	code, _, stderr = runHook(t, `{"tool_name":"kb_concept_write","tool_response":"`+joined+`"}`)
	if code != 2 || !strings.Contains(stderr, "1 finding(s)") {
		t.Fatalf("joined blocks: code %d:\n%s", code, stderr)
	}
	for _, name := range []string{"kb_execute", "kb_concept_read", "concept_writer"} {
		if code, _, stderr := runHook(t, `{"tool_name":"`+name+`","tool_response":"`+resp+`"}`); code != 0 || stderr != "" {
			t.Errorf("%s: code %d stderr %q, want silent", name, code, stderr)
		}
	}
}

// The Copilot CLI payload as probed on 1.0.94 (D676): camelCase fields, an MCP
// tool named <server>-<tool>, and textResultForLlm holding the response text
// with a second JSON object appended and no separator.
func TestWriteFindingsHook_CopilotChannel(t *testing.T) {
	resp := `{"findings":[{"path":"kb-a/page","check":"broken_link","message":"links to kb-a/missing"}]}{"last_error":"","sync_state":"pending"}`
	text, _ := json.Marshal(resp)
	payload := `{"sessionId":"s","timestamp":1,"cwd":"/work/project","toolName":"cartographer-concept_new","toolArgs":{},` +
		`"toolResult":{"resultType":"success","textResultForLlm":` + string(text) + `}}`

	code, stdout, stderr := runHookArgs(t, payload, "write-findings", "--channel", "copilot")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q, want 0 and silent stderr", code, stderr)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout, err)
	}
	if len(out) != 1 || !strings.Contains(out["additionalContext"], "1 finding(s)") || !strings.Contains(out["additionalContext"], "broken_link kb-a/page") {
		t.Errorf("additionalContext must be the only top-level key and carry the finding: %v", out)
	}

	// Every tool reaches the hook (no matcher): a tool that is not a write is silent.
	for _, name := range []string{"cartographer-concept_read", "bash", "cartographer-concept_writer"} {
		other := strings.Replace(payload, "cartographer-concept_new", name, 1)
		if code, stdout, stderr := runHookArgs(t, other, "write-findings", "--channel", "copilot"); code != 0 || stdout != "" || stderr != "" {
			t.Errorf("%s: code %d stdout %q stderr %q, want silent", name, code, stdout, stderr)
		}
	}
	// A write without findings is silent too.
	cleanText, _ := json.Marshal(`{"findings":[]}{"last_error":"","sync_state":"pending"}`)
	cleanPayload := `{"toolName":"cartographer-concept_write","toolResult":{"resultType":"success","textResultForLlm":` + string(cleanText) + `}}`
	if code, stdout, _ := runHookArgs(t, cleanPayload, "write-findings", "--channel", "copilot"); code != 0 || stdout != "" {
		t.Errorf("clean write: code %d stdout %q, want silent", code, stdout)
	}
}
