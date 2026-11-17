package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func noControl(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("control %U left in %q", r, s)
		}
	}
}

func TestBoundRemoteTextStripsEscapesAndControls(t *testing.T) {
	in := "remote: \x1b[31mred\x1b[0m \x1b]0;title\x07ok\rrewritten\x00\x7f‮evil⁩ \x9b31m"
	got := BoundRemoteText(in)
	noControl(t, got)
	for _, bad := range []string{"\x1b", "[31m", "title", "\r"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q survives in %q", bad, got)
		}
	}
	if !strings.Contains(got, "red") || !strings.Contains(got, "rewritten") {
		t.Fatalf("text lost: %q", got)
	}
}

func TestBoundRemoteTextCaps(t *testing.T) {
	long := BoundRemoteText(strings.Repeat("é", 5000))
	if utf8.RuneCountInString(long) > MaxRemoteLineRunes+1 {
		t.Fatalf("line not capped: %d runes", utf8.RuneCountInString(long))
	}
	var many []string
	for i := 0; i < 500; i++ {
		many = append(many, strings.Repeat("x", 100))
	}
	got := BoundRemoteText(strings.Join(many, "\n"))
	if len(got) > MaxRemoteTextBytes+len("…") || !utf8.ValidString(got) {
		t.Fatalf("total not capped: %d bytes", len(got))
	}
	if g := BoundRemoteLines(strings.Join(many, "\n"), 3); strings.Count(g, "|") != 2 {
		t.Fatalf("lines: %q", g)
	}
}

func TestPushErrorBoundsRemoteOutput(t *testing.T) {
	out := "remote: \x1b[2Jcleared\r\n" + strings.Repeat("a", 4000) + "\n" + strings.Repeat("b\n", 3000)
	for _, e := range []error{
		pushError("git push", errors.New("exit 1"), out),
		pushError("git push", errors.New("exit 1"), "fatal: Authentication failed\n"+out),
	} {
		msg := e.Error()
		noControl(t, msg)
		if len(msg) > 2*MaxRemoteTextBytes {
			t.Fatalf("error not bounded: %d bytes", len(msg))
		}
	}
}

func TestPermanentPushRejectionQuoteIsBounded(t *testing.T) {
	out := "remote: Permission to x denied \x1b[31m" + strings.Repeat("z", 1000) + "\r\n"
	remedy, ok := PermanentPushRejection(out)
	if !ok {
		t.Fatal("not a rejection")
	}
	noControl(t, remedy)
	if len(remedy) > 800 {
		t.Fatalf("quote not bounded: %d", len(remedy))
	}
}

// A credential is redacted before the caps cut the text: one that sits across
// the cap boundary must not survive as a prefix.
func TestRedactionRunsBeforeTheCap(t *testing.T) {
	const secret = "s3cretTOKENvalue99"
	remote := "https://user:" + secret + "@example.com/o/r.git"
	out := strings.Repeat("a", MaxRemoteLineRunes-10) + " " + remote + " tail"
	got := BoundRemoteText(RedactRemoteText(out, remote))
	if strings.Contains(got, "s3cret") || strings.Contains(got, secret) {
		t.Fatalf("credential fragment survives: %q", got)
	}
}

func TestBoundRemoteTextDropsFormatCharacters(t *testing.T) {
	got := BoundRemoteText("a\u200bb\u200c\u200dc\u2060d\ufeffe\U000E0041f\u202eg\u2066h")
	if got != "abcdefgh" {
		t.Fatalf("got %q", got)
	}
}

// The error's text is bounded; what classifies a refusal is the whole output.
func TestClassificationSurvivesTheBound(t *testing.T) {
	banner := strings.Repeat("remote: "+strings.Repeat("=", 80)+"\n", 30)
	cases := []struct {
		out   string
		check func(error) bool
	}{
		{banner + "remote: error: GH006: Protected branch update failed\n", func(e error) bool { return IsProtectedBranchRejection(RemoteText(e)) }},
		{banner + "remote: Permission to o/r denied to user.\n", func(e error) bool { _, ok := PermanentPushRejection(RemoteText(e)); return ok }},
		{strings.Repeat("x", 300) + " Could not resolve host: example.com\n", IsRemoteUnreachable},
	}
	for _, c := range cases {
		e := pushError("git push", errors.New("exit status 1"), c.out)
		if len(e.Error()) > 2*MaxRemoteTextBytes {
			t.Fatalf("message not bounded: %d", len(e.Error()))
		}
		if !c.check(e) {
			t.Fatalf("not classified through the bound: %q", e.Error())
		}
		if !c.check(fmt.Errorf("wrapped: %w", e)) {
			t.Fatal("not classified through a wrapper")
		}
	}
}

func TestScrubWriterSanitizesLiveLines(t *testing.T) {
	var buf bytes.Buffer
	w := &scrubWriter{w: &buf, remote: "https://example.com/o/r.git"}
	_, _ = w.Write([]byte("remote: \x1b[2Jhi\u202e\n" + strings.Repeat("y", 5000) + "\rprogress 50%\r"))
	w.Flush()
	got := buf.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\u202e") {
		t.Fatalf("live output carries %q", got)
	}
	for _, l := range strings.FieldsFunc(got, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if utf8.RuneCountInString(l) > MaxRemoteLineRunes+1 {
			t.Fatalf("live line not capped: %d", len(l))
		}
	}
	if !strings.Contains(got, "progress 50%\r") {
		t.Fatalf("progress terminator lost: %q", got)
	}
}
