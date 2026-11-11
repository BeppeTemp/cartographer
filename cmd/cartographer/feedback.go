package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/feedback"
	"github.com/BeppeTemp/cartographer/internal/secretscan"
)

const feedbackUsage = `usage: cartographer feedback --provider <name> --kind bug|idea [--title <text>]
                             [--file <path>|-] [--dir <folder>] [--output text|json] [--timeout 30s]

Prints a bug or idea report with every real name removed, and the link that
opens it prefilled in the operator's tracker. The draft is read from --file or
stdin. Nothing is sent and nothing is written: you open the link and submit it.

exit: 0 draft printed, 2 usage, 3 feedback is switched off by the operator,
      4 the draft is empty`

// maxFeedbackDraft bounds the draft read from --file or stdin.
const maxFeedbackDraft = 64 * 1024

// feedbackListLimit is the concept_list limit used to learn the concept IDs to
// remove; a longer KB is reported as truncated, the model pass covers the rest.
const feedbackListLimit = 500

// feedbackOut is the --output json shape.
type feedbackOut struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Removed     string   `json:"removed"`
	SearchURL   string   `json:"search_url,omitempty"`
	URL         string   `json:"url"`
	BodyInURL   bool     `json:"body_in_url"`
	Destination string   `json:"destination"`
	Notes       []string `json:"notes,omitempty"`
}

// cmdFeedback is `cartographer feedback`: the deterministic redactor an agent
// with a shell calls instead of redacting by hand (D1925).
//
// It sends nothing and writes nothing. Its only network use is Meridian, to read
// kb_status and concept_list; it opens no browser. See TestFeedbackWritesNothing.
func cmdFeedback(args []string) int {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "", "agent client the report comes from (required)")
	kind := fs.String("kind", "", "bug or idea (required)")
	title := fs.String("title", "", "report title")
	file := fs.String("file", "-", "draft file, or - for stdin")
	dir := fs.String("dir", "", "folder to resolve (default: the working directory)")
	output := fs.String("output", "text", "text or json")
	timeout := fs.Duration("timeout", 30*time.Second, "bound on the server calls")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, feedbackUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
		return 2
	}
	if *provider == "" || fs.NArg() > 0 || (*kind != "bug" && *kind != "idea") {
		fs.Usage()
		return 2
	}
	if *output != "text" && *output != "json" {
		return fail("--output must be text or json")
	}
	if !isKnownProvider(*provider) {
		return fail("unknown provider %q (valid: %s)", *provider, strings.Join(knownProviderNames(), ", "))
	}
	in := io.Reader(mcpStdin)
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return fail("%v", err)
		}
		defer f.Close()
		in = f
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxFeedbackDraft+1))
	if err != nil {
		return fail("read the draft: %v", err)
	}
	if len(raw) > maxFeedbackDraft {
		return fail("the draft is larger than %d KiB: shorten it", maxFeedbackDraft/1024)
	}
	if strings.TrimSpace(string(raw)) == "" {
		fmt.Fprintln(os.Stderr, "Error: the draft is empty")
		return 4
	}

	// Nothing the model wrote needs to be on the command line: without --title
	// the draft's first line, "# <title>", is the title and is peeled off.
	if first, rest, _ := strings.Cut(string(raw), "\n"); *title == "" && strings.HasPrefix(first, "# ") {
		*title, raw = strings.TrimSpace(first[2:]), []byte(rest)
	}
	f := gatherFeedbackFacts(*provider, *dir, *timeout)
	dest, err := feedback.ParseDestination(f.destination)
	if err != nil { // a template the server should have refused: do not guess
		dest, _ = feedback.ParseDestination("")
		f.notes = append(f.notes, "the destination setting is unreadable: the GitHub default is used")
	}
	if dest.Mode() == feedback.ModeOff {
		fmt.Fprintln(os.Stderr, "feedback reports are switched off by the operator of this server")
		return 3
	}

	// The kinds are read from the raw draft: Redact reports counts only.
	var kinds []string
	for _, m := range secretscan.Find(string(raw)) {
		kinds = append(kinds, m.Kind)
	}
	body, sum := feedback.Redact(f.stripToken(string(raw)), f.known)
	ttl, tsum := feedback.Redact(f.stripToken(*title), f.known)
	sum = addSummary(sum, tsum)
	if f.tokenHits > 0 {
		sum.Secrets += f.tokenHits
		kinds = append(kinds, "client token")
	}
	if !strings.Contains(strings.ToLower(body), "version and transport") {
		body = strings.TrimRight(body, "\n") + "\n\n" + feedback.ContextBlock(f.context(*provider))
	}
	rep := feedback.Report{Kind: *kind, Title: ttl, Body: body}
	submit, inURL := dest.SubmitURL(rep)

	out := feedbackOut{Title: ttl, Body: body, Removed: removedLine(sum, kinds), SearchURL: dest.SearchURL(ttl),
		URL: submit, BodyInURL: inURL, Destination: string(dest.Mode()), Notes: f.notes}
	if *output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	printFeedback(os.Stdout, out, f.latestNewer)
	return 0
}

// feedbackFacts is what the command learned about the installation.
type feedbackFacts struct {
	known                 feedback.Known
	destination           string
	serverVersion, latest string
	transport             string
	latestNewer           bool
	token                 string
	tokenHits             int
	notes                 []string
}

func (f *feedbackFacts) stripToken(s string) string {
	if len(f.token) < 8 || !strings.Contains(s, f.token) {
		return s
	}
	f.tokenHits += strings.Count(s, f.token)
	return strings.ReplaceAll(s, f.token, "[removed]")
}

func (f *feedbackFacts) context(provider string) feedback.Context {
	return feedback.Context{ClientVersion: version, ServerVersion: orUnknown(f.serverVersion), LatestVersion: f.latest,
		AgentClient: provider, Transport: f.transport, OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// gatherFeedbackFacts collects, best effort, every real value the draft must
// not carry. A source that fails is named in notes and the command goes on:
// it works with the server down (the destination is then GitHub).
func gatherFeedbackFacts(provider, dir string, timeout time.Duration) *feedbackFacts {
	f := &feedbackFacts{}
	k := &f.known
	if u, err := user.Current(); err == nil {
		k.User = u.Username
	}
	k.Home, _ = os.UserHomeDir()
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(dir); err == nil && dir != "" {
		k.Workspaces = append(k.Workspaces, abs)
	}
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		f.notes = append(f.notes, "not collected: the client configuration")
		return f
	}
	f.transport = "HTTP"
	f.token = resolveToken(cfg)
	if h := hostOfURL(cfg.ServerURL); h != "" {
		k.ServerHosts = append(k.ServerHosts, h, hostOnlyOf(h))
	}
	bound, _ := cfg.BoundKBs(provider)
	k.KBNames = append(k.KBNames, bound...)
	k.KBNames = append(k.KBNames, cfg.KnownKBs...)
	k.Workspaces = append(k.Workspaces, cfg.ScanRoots()...)
	for _, v := range cfg.Paths {
		k.Workspaces = append(k.Workspaces, v)
	}
	for _, bs := range cfg.Workspaces {
		for _, b := range bs {
			k.Workspaces = append(k.Workspaces, b.Path)
			k.KBNames = append(k.KBNames, b.KBs...)
			if h := hostOfURL(b.Remote); h != "" {
				k.ServerHosts = append(k.ServerHosts, h)
			}
		}
	}
	if cfg.ServerURL == "" {
		return f
	}
	// One deadline for the whole gather: each call gets the time left, so N KBs
	// cannot take N times --timeout.
	deadline := time.Now().Add(timeout)
	c := client.New(cfg.ServerURL, f.token).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnvName(cfg))
	health, err := c.WithHTTPTimeout(timeout).Health(min(timeout, probeTimeout))
	if err != nil {
		f.notes = append(f.notes, "not collected: the server ("+serverFailure(err)+")")
		return f
	}
	if health.KBs != nil {
		for _, kb := range *health.KBs {
			k.KBNames = append(k.KBNames, kb.Name)
		}
	}
	targets, err := resolveKBTargets(health, bound)
	if err != nil || len(targets) == 0 {
		f.notes = append(f.notes, "not collected: the server's KBs for this client")
		return f
	}
	for i, t := range targets {
		left := time.Until(deadline)
		if left <= 0 {
			f.notes = append(f.notes, "not collected: the concept IDs of the remaining KBs (--timeout reached)")
			break
		}
		tc := c.WithKB(t.Name).WithHTTPTimeout(left)
		if i == 0 { // the setting is server-wide: one kb_status says it
			f.readStatus(tc, t)
		}
		var list struct {
			Concepts []struct {
				ID string `json:"id"`
			} `json:"concepts"`
			Truncated bool `json:"truncated"`
		}
		raw, err := callTool(tc, t, "concept_list", map[string]any{"limit": feedbackListLimit})
		if err == nil {
			err = unwrapJSON(raw, &list)
		}
		if err != nil {
			f.notes = append(f.notes, "not collected: the concept IDs of a KB ("+serverFailure(err)+")")
			continue
		}
		for _, cn := range list.Concepts {
			k.ConceptIDs = append(k.ConceptIDs, cn.ID)
		}
		if list.Truncated {
			f.notes = append(f.notes, "a KB has more than 500 concepts: IDs beyond the first 500 are not known here, check the draft for them")
		}
	}
	return f
}

// readStatus fills the server facts from kb_status.
func (f *feedbackFacts) readStatus(c *client.MCPClient, t kbTarget) {
	var st struct {
		ServerVersion string `json:"server_version"`
		LatestVersion string `json:"latest_version"`
		Feedback      *struct {
			Destination string `json:"destination"`
			URLTemplate string `json:"url_template"`
		} `json:"feedback"`
	}
	raw, err := callTool(c, t, "kb_status", map[string]any{})
	if err == nil {
		err = unwrapJSON(raw, &st)
	}
	if err != nil {
		f.notes = append(f.notes, "not collected: the server version and destination ("+serverFailure(err)+")")
		return
	}
	f.serverVersion, f.latest = st.ServerVersion, st.LatestVersion
	f.latestNewer = st.LatestVersion != "" && st.LatestVersion != st.ServerVersion
	if st.Feedback != nil {
		f.destination = st.Feedback.Destination
		if st.Feedback.Destination == string(feedback.ModeCustom) {
			f.destination = st.Feedback.URLTemplate
		}
	}
}

// serverFailure words a failure without the response body or the token.
func serverFailure(err error) string {
	var re *client.RemoteError
	if errors.As(err, &re) {
		return string(re.Code)
	}
	return "request failed"
}

// unwrapJSON decodes a tool result that is a JSON object, or a list of text
// blocks whose first is one (the shape callInto reads).
func unwrapJSON(raw json.RawMessage, v any) error {
	var blocks []string
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 {
		raw = json.RawMessage(blocks[0])
	}
	return json.Unmarshal(raw, v)
}

func hostOfURL(s string) string {
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

func hostOnlyOf(h string) string {
	if u, err := url.Parse("//" + h); err == nil {
		return u.Hostname()
	}
	return h
}

func addSummary(a, b feedback.Summary) feedback.Summary {
	return feedback.Summary{Secrets: a.Secrets + b.Secrets, Hosts: a.Hosts + b.Hosts, Paths: a.Paths + b.Paths,
		Users: a.Users + b.Users, ConceptIDs: a.ConceptIDs + b.ConceptIDs, KBNames: a.KBNames + b.KBNames}
}

// removedLine counts what was replaced, per class and secret kind: never a value.
func removedLine(s feedback.Summary, kinds []string) string {
	var parts []string
	for _, c := range []struct {
		n    int
		name string
	}{{s.KBNames, "KB names"}, {s.Hosts, "hosts"}, {s.Paths, "paths"}, {s.Users, "users"}, {s.ConceptIDs, "concept IDs"}, {s.Secrets, "secrets"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.name))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	line := strings.Join(parts, ", ")
	if len(kinds) > 0 {
		sort.Strings(kinds)
		seen := kinds[:0]
		for _, k := range kinds {
			if len(seen) == 0 || seen[len(seen)-1] != k {
				seen = append(seen, k)
			}
		}
		line += " (secret kinds: " + strings.Join(seen, ", ") + ")"
	}
	return line
}

func printFeedback(w io.Writer, o feedbackOut, behind bool) {
	if behind {
		fmt.Fprintln(w, "The server is behind the latest release: update it and try again before reporting.")
	}
	fmt.Fprintf(w, "Title: %s\n\nBody:\n%s\n\nRemoved: %s\n", o.Title, strings.TrimRight(o.Body, "\n"), o.Removed)
	for _, n := range o.Notes {
		fmt.Fprintf(w, "Note: %s\n", n)
	}
	if o.SearchURL != "" {
		fmt.Fprintf(w, "\nSearch first: %s\n", o.SearchURL)
	}
	if o.BodyInURL {
		fmt.Fprintf(w, "\nSubmit: %s\n", o.URL)
	} else {
		fmt.Fprintf(w, "\nSubmit (the body did not fit the link: paste the body above): %s\n", o.URL)
	}
	fmt.Fprintln(w, "\nNothing has been sent: open the link and submit it.")
}
