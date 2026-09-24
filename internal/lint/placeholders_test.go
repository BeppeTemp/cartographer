package lint

import (
	"strings"
	"testing"
)

// D263: the registry checks.

const lintRegistry = "paths:\n  editor-config: {description: editor config, default: ~/.config/editor}\n" +
	"  claude-home: {description: Claude Code, default: $HOME/.claude}\n" +
	"  unused-one: {description: nobody cites this}\n" +
	"repos:\n  tools: {description: tools repo}\n"

func TestRun_NoRegistry_NoNewFindings(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\nSee {{path:anything}} and {{repo:whatever}}.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"unknown_placeholder", "unused_placeholder", "contract_malformed"} {
		if got := findingsOf(findings, check); len(got) != 0 {
			t.Errorf("%s without paths.yaml: %v", check, got)
		}
	}
}

// The acceptance of WP3: {{path:editor-home}} when only editor-config is
// declared is unknown_placeholder, once per concept however many times cited.
func TestRun_UnknownPlaceholder(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", lintRegistry)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\n{{path:editor-home}} twice {{path:editor-home}}, {{repo:tools}}, {{path:editor-config}}, {{repo:editor-config}}, {{\\path:escaped}}, {{path:<name>}}.\n")
	writeFile(t, k.DataRoot(), "arch/b.md", "---\ntype: Note\ntitle: B\n---\nOnly {{path:claude-home}}. Links [[arch/a]].\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "unknown_placeholder")
	if len(got) != 1 || got[0].Path != "arch/a.md" || got[0].Severity != SevWarning {
		t.Fatalf("unknown_placeholder = %+v", got)
	}
	// A key declared under the other kind is still undeclared.
	for _, want := range []string{"{{path:editor-home}}", "{{repo:editor-config}}"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q must name %s", got[0].Message, want)
		}
	}
	for _, not := range []string{"escaped", "<name>", "{{repo:tools}}", "{{path:editor-config}}"} {
		if strings.Contains(got[0].Message, not) {
			t.Errorf("message %q must not name %s", got[0].Message, not)
		}
	}
}

func TestRun_UnknownPlaceholder_Suppressible(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", lintRegistry)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [unknown_placeholder]\n---\nOld key {{path:editor-home}}.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "unknown_placeholder"); len(got) != 0 {
		t.Errorf("lint_ignore did not silence it: %v", got)
	}
	if got := findingsOf(findings, "lint_ignore_invalid"); len(got) != 0 {
		t.Errorf("unknown_placeholder must be a valid lint_ignore name: %v", got)
	}
}

func TestRun_UnusedPlaceholder(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", lintRegistry)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\n{{path:editor-config}} {{path:claude-home}}\n")
	// A key only an artifact cites is used.
	writeFile(t, k.Root, "skills/s/SKILL.md", "---\nname: s\ndescription: d\n---\nclone at {{repo:tools}}\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "unused_placeholder")
	if len(got) != 1 || got[0].Path != "paths.yaml" || got[0].Severity != SevInfo || !strings.Contains(got[0].Message, "path:unused-one") {
		t.Fatalf("unused_placeholder = %+v", got)
	}
	// KB-level: a scoped lint does not report it.
	scoped, err := Run(k, "arch", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(scoped, "unused_placeholder"); len(got) != 0 {
		t.Errorf("scoped lint reported a KB-level finding: %v", got)
	}
	// Not suppressible per concept.
	writeFile(t, k.DataRoot(), "arch/b.md", "---\ntype: Note\ntitle: B\nlint_ignore: [unused_placeholder]\n---\n[[arch/a]]\n")
	findings, _ = Run(k, "", false)
	invalid := findingsOf(findings, "lint_ignore_invalid")
	if len(invalid) != 1 || !strings.Contains(invalid[0].Message, "directory-level") {
		t.Errorf("lint_ignore of unused_placeholder: %v", invalid)
	}
}

func TestRun_MalformedRegistryEntry(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", lintRegistry+"  bad: {description: d, default: /Users/x/tools}\n")
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\n{{repo:tools}}\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "contract_malformed")
	if len(got) != 1 || got[0].Path != "paths.yaml" || !strings.Contains(got[0].Message, "repos.bad") {
		t.Fatalf("contract_malformed = %+v", got)
	}

	// A file that is not a registry at all: one finding, and no key is
	// flagged undeclared because of it.
	writeFile(t, k.Root, "paths.yaml", "- not a mapping\n")
	findings, _ = Run(k, "", false)
	if got := findingsOf(findings, "contract_malformed"); len(got) != 1 {
		t.Errorf("contract_malformed = %+v", got)
	}
	if got := findingsOf(findings, "unknown_placeholder"); len(got) != 0 {
		t.Errorf("an unparseable registry must not flag every key: %v", got)
	}
}

func TestRun_MachinePathSuggestsDeclaredKey(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", lintRegistry)
	cases := map[string]string{
		"~/.claude/settings.json":         "`{{path:claude-home}}/settings.json`",
		"/Users/user/.config/editor/x.md": "`{{path:editor-config}}/x.md`",
		"/home/user/.claude":              "`{{path:claude-home}}`",
		"~/.claudex/settings.json":        "",
		"~/elsewhere/file":                "",
	}
	i := 0
	for flagged, want := range cases {
		i++
		rel := "arch/c" + string(rune('a'+i)) + ".md"
		writeFile(t, k.DataRoot(), rel, "---\ntype: Note\ntitle: C\n---\nSee "+flagged+" here.\n")
		findings, err := Run(k, rel[:len(rel)-3], false)
		if err != nil {
			t.Fatal(err)
		}
		mp := findingsOf(findings, "machine_path")
		if len(mp) != 1 {
			t.Fatalf("%s: machine_path = %v", flagged, mp)
		}
		if want == "" {
			if strings.Contains(mp[0].Message, "declared in") {
				t.Errorf("%s: no prefix match, yet a suggestion: %s", flagged, mp[0].Message)
			}
			continue
		}
		if !strings.Contains(mp[0].Message, "use "+want) {
			t.Errorf("%s: message %q, want suggestion %s", flagged, mp[0].Message, want)
		}
	}
}
