// Package feedback builds the destination URL of a bug report an agent offers
// about Cartographer itself: where the report goes (Destination) and the URL
// that carries it (SubmitURL).
//
// It is a leaf: no I/O, no clock, no globals, and no import of kb, lint,
// mcpserver or search, so Compass and Meridian can both link it. Nothing here
// sends a report; the user opens the URL and submits it.
package feedback

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Repo is the public repository reports are filed against; a test pins it to
// the repo_url of mkdocs.yml.
const Repo = "BeppeTemp/cartographer"

// MaxURL is the longest submit URL that still carries the body: past it the
// URL carries the title only and the caller prints the body for pasting.
const MaxURL = 6000

// Mode is what a Destination does with a report.
type Mode string

// The three destinations of the feedback.destination setting.
const (
	ModeGitHub Mode = "github"
	ModeCustom Mode = "custom"
	ModeOff    Mode = "off"
)

// Destination is the parsed feedback.destination setting. The zero value is
// the default, GitHub.
type Destination struct {
	mode Mode
	tmpl string
}

// Report is a drafted report. Kind is "bug" or "idea"; anything else is a bug.
type Report struct{ Kind, Title, Body string }

var placeholderRE = regexp.MustCompile(`\{[^{}]*\}`)

// ParseDestination reads the setting: "github" (also ""), "off", or an URL
// template that is https:// (http:// only to a loopback host), has its host
// free of userinfo and placeholders, contains {title} and uses no placeholder
// but {kind}, {title} and {body}.
func ParseDestination(s string) (Destination, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "github":
		return Destination{mode: ModeGitHub}, nil
	case "off":
		return Destination{mode: ModeOff}, nil
	}
	bad := func(why string) (Destination, error) {
		return Destination{}, fmt.Errorf("feedback.destination: %s", why)
	}
	for _, p := range placeholderRE.FindAllString(s, -1) {
		if p != "{kind}" && p != "{title}" && p != "{body}" {
			return bad("unknown placeholder " + p + " (allowed: {kind}, {title}, {body})")
		}
	}
	if strings.ContainsAny(placeholderRE.ReplaceAllString(s, ""), "{}") {
		return bad("unbalanced brace")
	}
	if !strings.Contains(s, "{title}") {
		return bad("the template must contain {title}")
	}
	_, rest, _ := strings.Cut(s, "://")
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		rest = rest[:end]
	}
	if strings.ContainsAny(rest, "{}@") {
		return bad("the host must be literal and carry no userinfo")
	}
	u, err := url.Parse(placeholderRE.ReplaceAllString(s, "x"))
	if err != nil || u.Hostname() == "" || u.User != nil {
		return bad("not a valid URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
			return bad("http:// is allowed to a loopback host only")
		}
	default:
		return bad("the scheme must be https://")
	}
	return Destination{mode: ModeCustom, tmpl: s}, nil
}

// Mode reports github, custom or off.
func (d Destination) Mode() Mode {
	if d.mode == "" {
		return ModeGitHub
	}
	return d.mode
}

func esc(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func kindOf(r Report) string {
	if r.Kind == "idea" {
		return "idea"
	}
	return "bug"
}

// SubmitURL builds the URL the user opens. bodyInURL is false when the body is
// not in it (it did not fit in MaxURL, or the custom template has no {body}):
// the caller then prints the body. An off destination has no URL.
func (d Destination) SubmitURL(r Report) (u string, bodyInURL bool) {
	tmpl := "https://github.com/" + Repo + "/issues/new?template={kind}.md&title={title}&body={body}"
	switch d.Mode() {
	case ModeOff:
		return "", false
	case ModeCustom:
		tmpl = d.tmpl
	}
	build := func(title, body string) string {
		return strings.NewReplacer("{kind}", kindOf(r), "{title}", esc(title), "{body}", esc(body)).Replace(tmpl)
	}
	if u = build(r.Title, r.Body); len(u) <= MaxURL && strings.Contains(tmpl, "{body}") {
		return u, true
	}
	// Without the body the title alone must still fit: cut it, a rune at a time.
	title := []rune(r.Title)
	for u = build(string(title), ""); len(u) > MaxURL && len(title) > 0; u = build(string(title), "") {
		title = title[:max(0, len(title)-max(1, (len(u)-MaxURL)/12))]
	}
	return u, false
}

// SearchURL is the issue search for a title, to look for a duplicate first;
// empty unless the destination is GitHub.
func (d Destination) SearchURL(title string) string {
	if d.Mode() != ModeGitHub {
		return ""
	}
	return "https://github.com/" + Repo + "/issues?q=" + url.QueryEscape("is:issue "+title)
}

// Context is what the caller knows about the installation, free of names.
type Context struct {
	ClientVersion, ServerVersion, LatestVersion, AgentClient, Transport, OS, Arch string
}

// ContextBlock renders the non-empty facts, one line each, for the report body.
func ContextBlock(c Context) string {
	var b strings.Builder
	for _, f := range []struct{ label, v string }{
		{"client version", c.ClientVersion}, {"server version", c.ServerVersion},
		{"latest version", c.LatestVersion}, {"agent client", c.AgentClient},
		{"transport", c.Transport}, {"os/arch", strings.TrimSpace(c.OS + " " + c.Arch)},
	} {
		if v := strings.Join(strings.Fields(f.v), " "); v != "" {
			b.WriteString("- " + f.label + ": " + v + "\n")
		}
	}
	return b.String()
}
