package lint

import (
	"strings"
	"testing"
)

const extBody = "# Doc\n\nTBD: fill in.\nSee ~/venv/bin for the env.\nBroken [link](missing.md).\n"

func extPage(origin, title string) string {
	fm := "---\ntype: Note\ntitle: " + title + "\n"
	if origin != "" {
		fm += "origin: " + origin + "\n"
	}
	return fm + "---\n" + extBody
}

// #1195: origin: external skips the three content checks, nothing else.
func TestExternalOrigin_SkipsContentChecks(t *testing.T) {
	t.Parallel()
	long := "✨ " + strings.Repeat("A very long verbatim title ", 8)
	for _, tc := range []struct {
		name, origin string
		skipped      bool
	}{
		{"external", "external", true},
		{"external cased", " External ", true},
		{"other value", "internal", false},
		{"absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "kb-a/p.md", extPage(tc.origin, long))
			findings, err := Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range []string{"open_marker", "machine_path", "title_quality"} {
				if got := hasCheck(findings, "kb-a/p.md", c); got == tc.skipped {
					t.Errorf("%s: present=%v, want %v", c, got, !tc.skipped)
				}
			}
			if !hasCheck(findings, "kb-a/p.md", "broken_link") {
				t.Errorf("structural broken_link must still fire: %v", findings)
			}
		})
	}
}

func TestExternalOrigin_WritePath(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	has := func(fs []Finding, c string) bool {
		for _, f := range fs {
			if f.Check == c {
				return true
			}
		}
		return false
	}
	long := "✨ " + strings.Repeat("A very long verbatim title ", 8)
	plain := CheckConcept(k, "kb-a/p", extPage("", long))
	ext := CheckConcept(k, "kb-a/p", extPage("external", long))
	for _, c := range []string{"open_marker", "machine_path", "title_quality"} {
		if !has(plain, c) {
			t.Errorf("without origin, %s missing: %+v", c, plain)
		}
		if has(ext, c) {
			t.Errorf("with origin: external, %s present: %+v", c, ext)
		}
	}
}

func TestMachinePath_RemoteFencedBlocks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		flagged    bool
	}{
		{"preceding ssh line", "On the GPU server (`ssh gpu-01`):\n\n```bash\ncd /home/user/work\n```\n", false},
		{"preceding ssh user@host", "Run ssh user@host.example.com then:\n```\nls ~/data\n```\n", false},
		{"first line ssh", "```\nssh gpu-01\ncd /home/user/work\n```\n", false},
		{"user@host$ prompt", "```\nuser@gpu-01:~/proj$ cat /home/user/x\n```\n", false},
		{"user@host$ bare", "```\nuser@gpu-01$ ls ~/x\n```\n", false},
		{"bracket prompt", "```\n[user@gpu-01 proj]$ ls ~/x\n```\n", false},
		{"root prompt", "```\nroot@gpu-01:/srv# ls ~/x\n```\n", false},
		{"bare dollar prompt", "```\n$ cd /home/user/work\n```\n", true},
		{"no ssh context", "Local setup:\n```\ncd /home/user/work\n```\n", true},
		{"prose after remote block", "```\nssh gpu-01\nls\n```\n\nBut /home/user/here is local.\n", true},
		{"next block not remote", "```\nssh gpu-01\nls\n```\n\n```\ncd /home/user/work\n```\n", true},
		{"second block after ssh line", "run on `ssh gpu-01`:\n```\nls\n```\n\n```\ncd /home/user/proj\n```\n", true},
		{"ssh line blocked by text between", "ssh gpu-01\n\nSome other text.\n```\ncd /home/user/work\n```\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := len(disallowedMachinePaths(tc.body)) > 0
			if got != tc.flagged {
				t.Errorf("flagged=%v, want %v for %q", got, tc.flagged, tc.body)
			}
		})
	}
}

func TestMachinePath_MessageSaysHowToExpressRemote(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	fs := CheckConcept(k, "kb-a/p", "---\ntype: Note\ntitle: T\n---\nAt /home/alice/x.\n")
	for _, f := range fs {
		if f.Check == "machine_path" {
			for _, want := range []string{"ssh <host>", "user@host$", "origin: external"} {
				if !strings.Contains(f.Message, want) {
					t.Errorf("message lacks %q: %s", want, f.Message)
				}
			}
			return
		}
	}
	t.Fatal("no machine_path finding")
}
