package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// skillInvocation matches a `cartographer <sub> [<action>]` a bundled skill
// tells the agent to run: in a code span, at the start of a line (a code
// block), or after an opening parenthesis. Prose such as "a Cartographer
// server" never matches: the name is lowercase and followed by a command word.
var skillInvocation = regexp.MustCompile("(?m)(?:`|^[ \t]*(?:\\$ )?|\\()cartographer ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?")

// usageOf is the usage text of each command that has actions, taken from the
// command's own usage printer, so a renamed or removed action fails here.
func usageOf(t *testing.T) map[string]string {
	t.Helper()
	capture := func(f func(io.Writer)) string {
		var b bytes.Buffer
		f(&b)
		return b.String()
	}
	out := map[string]string{
		"kb":      capture(printKBUsage),
		"service": capture(printServiceUsage),
		"paths":   capture(printPathsUsage),
		"update":  capture(printUpdateUsage),
	}
	f, err := os.CreateTemp(t.TempDir(), "usage")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	printClientUsage(f)
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	out["client"] = string(b)
	return out
}

// TestBundledSkillsNameRealCommands guards the one thing a bundled skill
// cannot check itself: that every `cartographer <command>` it tells an agent
// to run exists. A command is a top-level subcommand of `cartographer help`
// (plus the internal `hook`); an action of kb, service, paths, update or
// client must appear in that command's usage. The other commands (plugin,
// surveyor, ...) are checked at the first level only.
func TestBundledSkillsNameRealCommands(t *testing.T) {
	top := map[string]bool{"hook": true}
	for _, s := range subcommands {
		top[s.name] = true
	}
	usages := usageOf(t)
	seen := 0
	err := fs.WalkDir(skillbundle.FS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".md" {
			return err
		}
		data, rerr := fs.ReadFile(skillbundle.FS, p)
		if rerr != nil {
			return rerr
		}
		for _, m := range skillInvocation.FindAllStringSubmatch(string(data), -1) {
			seen++
			if !top[m[1]] {
				t.Errorf("%s: `cartographer %s` is not a command (see `cartographer help`)", p, m[1])
				continue
			}
			usage, hasActions := usages[m[1]]
			if m[2] == "" || !hasActions {
				continue
			}
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(m[2]) + `\b`).MatchString(usage) {
				t.Errorf("%s: `cartographer %s %s` is not in the usage of `cartographer %s`:\n%s",
					p, m[1], m[2], m[1], strings.TrimSpace(usage))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no `cartographer <command>` found in the bundled skills: the pattern no longer matches them")
	}
}
