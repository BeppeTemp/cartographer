package provisioning

// D216 WP3: the hook command line must be executable as written, on both
// platforms. These are internal tests because resolveHookCommand is where the
// two defects lived — a split on the first space and a "/"-only notion of
// relative — and both are invisible from outside: the malformed command reaches
// settings.json looking plausible and fails only at session start.
//
// Where the assertion is about a path, the hook directory is built with
// filepath.Join so it carries the host's separators: filepath.ToSlash — which
// commandOwnedBy relies on — is a no-op on unix, so a hard-coded `C:\…` literal
// would test a translation that only happens on Windows and fail here for a
// reason that has nothing to do with the code. A directory segment containing a
// space is the part that reproduces the real defect, and it reproduces it on
// every host.

import (
	"path/filepath"
	"strings"
	"testing"
)

// hookDirWithASpace is the shape that broke: a hook materialized under a home
// directory named after a person ("C:\Users\Nome Cognome\…" is the Windows
// original), in the host's own spelling.
func hookDirWithASpace(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "Nome Cognome", ".claude", "hooks", "notify")
}

// hookDir is a materialized hook directory in the host's own spelling, drive and
// all. A hard-coded "/home/…" literal is not one on Windows: filepath.Join makes
// it "\home\…", which is rooted but has no volume, so neither filepath.IsAbs
// nor isAbsCommandPath calls it absolute and a second pass joins it again. That
// is a property of the literal, not of the code — a real base dir is derived from
// the home directory and always carries a drive — so the cases that exercise
// joining use a real one.
func hookDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".claude", "hooks", "notify")
}

func TestResolveHookCommand(t *testing.T) {
	// Left-alone cases only: nothing is joined to this, so its spelling is inert.
	const posixDir = "/home/beppe/.claude/hooks/notify"
	baseDir := hookDir(t)
	spacedDir := hookDirWithASpace(t)

	cases := []struct {
		name    string
		command string
		hookDir string
		want    string
	}{
		{
			name:    "relative slash command is resolved against the hook dir",
			command: "./notify.sh",
			hookDir: baseDir,
			want:    filepath.Join(baseDir, "notify.sh"),
		},
		{
			// Before, "relative" meant "contains a /", so this was left verbatim
			// and the hook never ran. filepath.Join keeps the host's separators —
			// what matters is that it was joined at all.
			name:    "a backslash-declared command is relative too",
			command: `scripts\run.sh`,
			hookDir: baseDir,
			want:    filepath.Join(baseDir, `scripts\run.sh`),
		},
		{
			name:    "a bare name is left to PATH",
			command: "jq",
			hookDir: posixDir,
			want:    "jq",
		},
		{
			name:    "a $VAR reference is left alone",
			command: "$HOME/bin/notify.sh --flag",
			hookDir: posixDir,
			want:    "$HOME/bin/notify.sh --flag",
		},
		{
			name:    "arguments survive verbatim",
			command: "./notify.sh --one --two=x",
			hookDir: baseDir,
			want:    filepath.Join(baseDir, "notify.sh") + " --one --two=x",
		},
		{
			// The defect: the resolved path contains a space, and the old code
			// split the command line on the first one — registering a prefix that
			// names no file.
			name:    "a resolved path containing a space is quoted, not truncated",
			command: "./notify.sh",
			hookDir: spacedDir,
			want:    `"` + filepath.Join(spacedDir, "notify.sh") + `"`,
		},
		{
			name:    "a quoted resolved path keeps its arguments after the quote",
			command: "./notify.sh --flag",
			hookDir: spacedDir,
			want:    `"` + filepath.Join(spacedDir, "notify.sh") + `" --flag`,
		},
		{
			// isAbsCommandPath, not filepath.IsAbs: on unix the latter calls a
			// drive-absolute path relative and joins it to the hook dir, turning a
			// KB's Windows command into a path under .claude/hooks.
			name:    "a drive-absolute command is absolute on every host",
			command: `C:\tools\notify.exe`,
			hookDir: posixDir,
			want:    `C:\tools\notify.exe`,
		},
		{
			name:    "a UNC command is absolute on every host",
			command: `\\host\share\notify.exe`,
			hookDir: posixDir,
			want:    `\\host\share\notify.exe`,
		},
		{
			// An absolute command whose own path contains a space must be quoted
			// in hook.json: unquoted, "C:\Program Files\x.exe --flag" is
			// indistinguishable from the command "C:\Program" with arguments, and
			// no amount of resolving can tell them apart. Quoted, it round-trips.
			name:    "an already-quoted absolute command round-trips",
			command: `"C:\Program Files\notify.exe" --flag`,
			hookDir: posixDir,
			want:    `"C:\Program Files\notify.exe" --flag`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveHookCommand(c.command, c.hookDir)
			if got != c.want {
				t.Fatalf("resolveHookCommand(%q, %q) = %q, want %q", c.command, c.hookDir, got, c.want)
			}
			// registerHookSettings runs on every connect/sync, so the function is
			// applied to material it may have produced itself: quoting an
			// already-quoted command would corrupt it silently.
			if again := resolveHookCommand(got, c.hookDir); again != got {
				t.Errorf("not idempotent: second pass = %q, want %q", again, got)
			}
		})
	}
}

// The quoting must not break the ownership marker: it is the only thing that
// makes registration idempotent and pruning possible (D57). commandOwnedBy
// matches it as a substring of the command's slash form, and the quotes sit at
// the ends, so they cannot get in the way — but nothing said so before.
func TestQuotedCommandIsStillRecognisedAsOurs(t *testing.T) {
	command := resolveHookCommand("./notify.sh", hookDirWithASpace(t))
	if !strings.HasPrefix(command, `"`) {
		t.Fatalf("command %q is not quoted: the rest of this test asserts nothing", command)
	}
	if marker := hookOwnershipMarker("notify"); !commandOwnedBy(command, marker) {
		t.Errorf("commandOwnedBy(%q, %q) = false: registration would append a POSIX comment and prune would find nothing", command, marker)
	}
}

// stripHookEntries drives prune off the same marker, so a quoted command written
// by a previous sync must still be removable.
func TestStripHookEntriesRemovesAQuotedCommand(t *testing.T) {
	command := resolveHookCommand("./notify.sh", hookDirWithASpace(t))
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PostToolUse": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": command},
				}},
			},
		},
	}
	if !stripHookEntries(settings, "notify") {
		t.Fatal("stripHookEntries reported no change: a quoted command is not recognised as ours")
	}
	if _, still := settings["hooks"]; still {
		t.Errorf("settings = %v, want the emptied hooks key dropped", settings)
	}
}

func TestSplitHookCommand(t *testing.T) {
	for _, c := range []struct {
		in, bin, rest string
		hasRest       bool
	}{
		{in: "jq", bin: "jq"},
		{in: "jq -r .x", bin: "jq", rest: "-r .x", hasRest: true},
		// Two spaces stay two spaces: the rest of the line is passed through
		// verbatim, as it was before quoting existed.
		{in: "jq  -r", bin: "jq", rest: " -r", hasRest: true},
		{in: `"/a b/x.sh"`, bin: `"/a b/x.sh"`},
		{in: `"/a b/x.sh" --flag`, bin: `"/a b/x.sh"`, rest: "--flag", hasRest: true},
		// An unbalanced quote is not something to guess at: fall back to the
		// space split rather than swallow the rest of the line.
		{in: `"/a b/x.sh --flag`, bin: `"/a`, rest: `b/x.sh --flag`, hasRest: true},
	} {
		bin, rest, hasRest := splitHookCommand(c.in)
		if bin != c.bin || rest != c.rest || hasRest != c.hasRest {
			t.Errorf("splitHookCommand(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, bin, rest, hasRest, c.bin, c.rest, c.hasRest)
		}
	}
}
