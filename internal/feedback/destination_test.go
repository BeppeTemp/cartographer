package feedback

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestRepoMatchesMkdocs(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../mkdocs.yml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^repo_url:\s*https://github.com/(\S+)\s*$`).FindSubmatch(b)
	if m == nil || string(m[1]) != Repo {
		t.Fatalf("Repo %q differs from mkdocs.yml repo_url %q", Repo, m)
	}
}

func TestParseDestination(t *testing.T) {
	t.Parallel()
	for in, mode := range map[string]Mode{
		"": ModeGitHub, "github": ModeGitHub, " GitHub ": ModeGitHub, "off": ModeOff,
		"https://tracker.example.com/new?t={title}":             ModeCustom,
		"https://tracker.example.com/{kind}?t={title}&b={body}": ModeCustom,
		"http://localhost:8080/new?t={title}":                   ModeCustom,
		"http://127.0.0.1/new?t={title}":                        ModeCustom,
	} {
		d, err := ParseDestination(in)
		if err != nil || d.Mode() != mode {
			t.Errorf("%q: %v %v", in, d.Mode(), err)
		}
	}
	for _, in := range []string{
		"https://tracker.example.com/new?t={foo}&x={title}",
		"ftp://tracker.example.com/{title}",
		"https://tracker.example.com/new?b={body}",
		"https://user@tracker.example.com/{title}",
		"https://user:pw@tracker.example.com/{title}",
		"http://tracker.example.com/{title}",
		"https://{title}.example.com/",
		"https://tracker.example.com/{title",
		"https:///{title}",
		"tracker.example.com/{title}",
		"mailto:a@example.com?subject={title}",
	} {
		if _, err := ParseDestination(in); err == nil || !strings.Contains(err.Error(), "feedback.destination") {
			t.Errorf("%q accepted or unnamed error: %v", in, err)
		}
	}
}

func TestSubmitURL(t *testing.T) {
	t.Parallel()
	gh, _ := ParseDestination("")
	u, in := gh.SubmitURL(Report{Kind: "idea", Title: "a b&c", Body: "line 1\nline 2"})
	want := "https://github.com/" + Repo + "/issues/new?template=idea.md&title=a%20b%26c&body=line%201%0Aline%202"
	if u != want || !in {
		t.Fatalf("got %q %v", u, in)
	}
	if u, _ := gh.SubmitURL(Report{Kind: "other", Title: "t"}); !strings.Contains(u, "template=bug.md") {
		t.Errorf("unknown kind is not a bug: %q", u)
	}
	long := Report{Kind: "bug", Title: "t", Body: strings.Repeat("x", MaxURL)}
	if u, in := gh.SubmitURL(long); in || strings.Contains(u, "xxx") || !strings.Contains(u, "title=t") || len(u) > MaxURL {
		t.Errorf("long body: %q %v", u, in)
	}
	edge := Report{Kind: "bug", Title: "t", Body: strings.Repeat("x", MaxURL-len("https://github.com/"+Repo+"/issues/new?template=bug.md&title=t&body="))}
	if u, in := gh.SubmitURL(edge); !in || len(u) != MaxURL {
		t.Errorf("exactly at the cap: %d %v", len(u), in)
	}
	huge := Report{Kind: "bug", Title: strings.Repeat("\U0001F600", 3000), Body: "b"}
	if u, in := gh.SubmitURL(huge); in || len(u) > MaxURL || !strings.Contains(u, "title=%F0%9F%98%80") || strings.HasSuffix(u, "%F0%9F%98") {
		t.Errorf("huge title: %d %v", len(u), in)
	}
	c, _ := ParseDestination("https://tracker.example.com/new?k={kind}&t={title}&b={body}")
	if u, in := c.SubmitURL(Report{Kind: "bug", Title: "a b", Body: "c"}); u != "https://tracker.example.com/new?k=bug&t=a%20b&b=c" || !in {
		t.Errorf("custom: %q %v", u, in)
	}
	if u, in := c.SubmitURL(long); in || strings.HasSuffix(u, "xxx") || !strings.HasSuffix(u, "&b=") {
		t.Errorf("custom long: %q %v", u[:min(len(u), 80)], in)
	}
	nb, _ := ParseDestination("https://tracker.example.com/new?t={title}")
	if u, in := nb.SubmitURL(Report{Title: "t", Body: "b"}); in || strings.Contains(u, "b=") {
		t.Errorf("no {body}: %q %v", u, in)
	}
	off, _ := ParseDestination("off")
	if u, in := off.SubmitURL(Report{Title: "t"}); u != "" || in {
		t.Errorf("off: %q", u)
	}
}

func TestSearchURL(t *testing.T) {
	t.Parallel()
	gh, _ := ParseDestination("github")
	if got := gh.SearchURL("a b"); got != "https://github.com/"+Repo+"/issues?q=is%3Aissue+a+b" {
		t.Errorf("got %q", got)
	}
	c, _ := ParseDestination("https://tracker.example.com/?t={title}")
	if c.SearchURL("x") != "" {
		t.Error("custom destination has no search URL")
	}
}

func TestContextBlock(t *testing.T) {
	t.Parallel()
	got := ContextBlock(Context{ClientVersion: "1.2.3", AgentClient: "agent\nx", OS: "linux", Arch: "amd64"})
	if got != "- client version: 1.2.3\n- agent client: agent x\n- os/arch: linux amd64\n" {
		t.Errorf("got %q", got)
	}
}
