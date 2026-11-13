package mcpprobe

import "testing"

func TestNewScrub(t *testing.T) {
	s := NewScrub([]string{"Bearer abcd1234", "abcd1234", "", "xy"})
	for in, want := range map[string]string{
		"401 for token abcd1234":       "401 for token <redacted>",
		"sent Bearer abcd1234 to host": "sent <redacted> to host",
		"nothing here":                 "nothing here",
		"has xy inside":                "<redacted>", // a piece too short to replace masks the text
	} {
		if got := s(in); got != want {
			t.Errorf("scrub(%q) = %q, want %q", in, got, want)
		}
	}
	if got := NewScrub(nil)("plain"); got != "plain" {
		t.Errorf("no values: %q", got)
	}
}
