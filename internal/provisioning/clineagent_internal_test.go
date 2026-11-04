package provisioning

import "testing"

// Cline rejects an empty system prompt and skips the file without a word: a
// body that is blank falls back to the description. The extension is pinned by
// the destination matrix test (.yaml).
func TestTranslateAgentForCline(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "---\nname: empty\ndescription: Reviews code\n---\n\n  \n", "---\nname: empty\ndescription: Reviews code\n---\nReviews code\n"},
		{"bare", "Just a prompt.\n", "---\nname: bare\ndescription: bare\n---\nJust a prompt.\n"},
	} {
		got, err := translateAgentForCline(tc.name, []byte(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}
