package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// runDispatch runs `hook dispatch` with a payload on stdin and a fake home,
// returning the exit code and the one line it printed.
func runDispatch(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	old := hookStdin
	defer func() { hookStdin = old }()
	hookStdin = strings.NewReader(stdin)
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	code := cmdHook(append([]string{"dispatch"}, args...))
	w.Close()
	os.Stdout = oldOut
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return code, strings.TrimSpace(buf.String())
}

func TestHookDispatchIsAlwaysExitZeroWithJSON(t *testing.T) {
	for name, args := range map[string][]string{
		"garbage payload":  {"--provider", "cline", "--event", "PreToolUse"},
		"unknown provider": {"--provider", "nope", "--event", "PreToolUse"},
		"unknown event":    {"--provider", "cline", "--event", "Nope"},
		"no flags":         {},
		"dangling flag":    {"--provider"},
	} {
		code, out := runDispatch(t, "not json", args...)
		if code != 0 || out != "{}" {
			t.Errorf("%s: code %d out %q", name, code, out)
		}
	}
}

func TestAsciiJSON(t *testing.T) {
	got := asciiJSON(struct {
		M string `json:"errorMessage"`
	}{"è<😀"})
	if want := `{"errorMessage":"\u00e8\u003c\ud83d\ude00"}`; got != want {
		t.Errorf("asciiJSON = %s, want %s", got, want)
	}
	for _, r := range got {
		if r > 0x7f {
			t.Fatalf("non-ASCII rune in %s", got)
		}
	}
}
