package provisioning

// D216 WP1: one portable notion of "executable" on the read-back side.
//
// The writer records MaterializedHash from its declared intent; the verifier
// recomputes it from disk. Where the filesystem has no execute bit the two used
// to disagree for every kind whose flag comes from the KB rather than from the
// kind — a skill shipping an executable helper — and the disagreement is
// permanent: no repair clears it, and `doctor` reporting drift nobody can fix
// teaches the operator to stop reading `doctor`.
//
// execBitSupported is the seam that makes the other platform's branch reachable
// from here. Without it this whole file would only ever run one of the two paths,
// and the broken one is the one that never runs.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/execbit"
)

// withExecBitSupport forces the branch executableOnDisk takes, both ways,
// whatever host the test runs on.
func withExecBitSupport(t *testing.T, supported bool) {
	t.Helper()
	previous := execBitSupported
	execBitSupported = supported
	t.Cleanup(func() { execBitSupported = previous })
}

// The regression that matters: ContentHashFiles must keep producing the same
// bytes. It is the hash the server publishes as Artifact.ContentHash and the one
// a client's expansion reproduces when an artifact carries no placeholder (D75
// WP3, D138) — change it and every installation on earth re-materializes at once
// and reports drift on the way. The fixture includes an executable file, because
// that flag is what this WP is about and the temptation was to normalise it
// inside the digest.
func TestContentHashFilesIsFrozen(t *testing.T) {
	const want = "97eccf24a5e4b0d9ff202338846a6924b3b0ccb413d250b23c1fd4ac7aeda7f4"
	got := ContentHashFiles([]ArtifactFile{
		{Path: "SKILL.md", Content: []byte("---\nname: fixture\n---\nBody.\n")},
		{Path: "scripts/check.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Executable: true},
		{Path: "scripts/notes.txt", Content: []byte("plain\n")},
	})
	if got != want {
		t.Fatalf("ContentHashFiles = %q, want %q\n"+
			"The artifact hash changed. It is a wire and on-disk contract: every client's "+
			"expanded hash stops matching the server's ContentHash, every artifact looks "+
			"drifted and gets rewritten. If the change is intended it is a migration, not a fix.", got, want)
	}
	// And the flag is genuinely part of the digest — otherwise the fixture above
	// would prove nothing about it.
	same := ContentHashFiles([]ArtifactFile{
		{Path: "SKILL.md", Content: []byte("---\nname: fixture\n---\nBody.\n")},
		{Path: "scripts/check.sh", Content: []byte("#!/bin/sh\nexit 0\n")},
		{Path: "scripts/notes.txt", Content: []byte("plain\n")},
	})
	if same == got {
		t.Error("the executable flag does not change the hash: the fixture cannot detect a normalisation inside the digest")
	}
}

func TestExecutableOnDisk(t *testing.T) {
	t.Run("with an execute bit the filesystem is the authority", func(t *testing.T) {
		withExecBitSupport(t, true)
		// An external chmod is real drift on unix, and reporting it is the point:
		// the recorded intent must not override what is actually on disk.
		mf := ManagedFile{Kind: "skill", ExecutablePaths: []string{"scripts/check.sh"}}
		if mf.executableOnDisk("scripts/check.sh", false) {
			t.Error("a skill file chmod-ed non-executable was reported executable from the lockfile")
		}
		if !mf.executableOnDisk("scripts/check.sh", true) {
			t.Error("an executable skill file was reported non-executable")
		}
		// The hook floor still applies over whatever the disk says.
		hook := ManagedFile{Kind: "hook"}
		if !hook.executableOnDisk("run.sh", false) {
			t.Error("a hook script must be executable by definition")
		}
		if hook.executableOnDisk("hook.json", true) {
			t.Error("hook.json must never be executable")
		}
	})

	t.Run("without an execute bit the lockfile is the authority", func(t *testing.T) {
		withExecBitSupport(t, false)
		mf := ManagedFile{Kind: "skill", ExecutablePaths: []string{"scripts/check.sh"}}
		// The mode says nothing here, so it is ignored in both directions.
		if !mf.executableOnDisk("scripts/check.sh", false) {
			t.Error("a recorded executable path was not reported executable")
		}
		if mf.executableOnDisk("scripts/notes.txt", true) {
			t.Error("a path absent from the lockfile was reported executable")
		}
		// A lockfile written before executable_paths existed has no field: the
		// fallback is exact for a hook and matches what that lockfile already
		// meant for every other kind.
		legacyHook := ManagedFile{Kind: "hook"}
		if !legacyHook.executableOnDisk("run.sh", false) {
			t.Error("a pre-field hook entry lost its executable floor")
		}
		if legacyHook.executableOnDisk("hook.json", false) {
			t.Error("hook.json must never be executable")
		}
		legacySkill := ManagedFile{Kind: "skill"}
		if legacySkill.executableOnDisk("scripts/check.sh", false) {
			t.Error("a pre-field skill entry invented an executable flag")
		}
	})
}

// The end-to-end property, both ways: what Apply recorded as MaterializedHash is
// what the verifier recomputes from disk. Simulating the platform where the mode
// cannot round-trip is the only way to see the failure from a unix host — and
// there the recorded intent is all there is to go on.
func TestRecomputedHashMatchesTheWriterOnEitherPlatform(t *testing.T) {
	cases := []struct {
		name string
		kind string
		// files as the KB declares them: path → executable
		files map[string]bool
	}{
		{
			name:  "hook: the flag is a pure function of kind and path",
			kind:  "hook",
			files: map[string]bool{"hook.json": false, "run.sh": true},
		},
		{
			// The residual gap. Nothing about a skill says which of its files is
			// executable; only the KB does, and only the lockfile carries it over.
			name:  "skill with an executable helper",
			kind:  "skill",
			files: map[string]bool{"SKILL.md": false, "scripts/check.sh": true},
		},
		{
			name:  "skill with nothing executable",
			kind:  "skill",
			files: map[string]bool{"SKILL.md": false},
		},
	}

	for _, c := range cases {
		for _, supported := range []bool{true, false} {
			if supported && !execbit.Supported {
				// Only one direction can be simulated: pretending the filesystem
				// is authoritative where it cannot report a mode asserts the
				// platform, not the code. The other direction — the one that was
				// broken — runs on every host.
				continue
			}
			name := c.name
			if supported {
				name += " (with an execute bit)"
			} else {
				name += " (without an execute bit)"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				var declared []ArtifactFile
				var execPaths []string
				for rel, exe := range c.files {
					full := filepath.Join(dir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						t.Fatal(err)
					}
					content := []byte("content of " + rel + "\n")
					mode := os.FileMode(0o644)
					// The floor the writer applies, and the mode it writes.
					effective := effectiveExecutable(c.kind, rel, exe)
					if effective {
						mode = 0o755
						execPaths = append(execPaths, rel)
					}
					if err := os.WriteFile(full, content, mode); err != nil {
						t.Fatal(err)
					}
					declared = append(declared, ArtifactFile{Path: rel, Content: content, Executable: effective})
				}
				// Exactly what copyArtifactFiles records.
				writerHash := hashArtifactFiles(declared)
				mf := ManagedFile{Kind: c.kind, Name: "fixture", MaterializedHash: writerHash, ExecutablePaths: execPaths}

				withExecBitSupport(t, supported)
				onDisk, err := contentHashDirManaged(mf, dir)
				if err != nil {
					t.Fatalf("contentHashDirManaged: %v", err)
				}
				if onDisk != writerHash {
					t.Errorf("recomputed %q, writer recorded %q — this is drift no repair can clear", onDisk, writerHash)
				}
				// The repair path reads the same files and must agree too.
				files, err := readManagedFiles(mf, dir)
				if err != nil {
					t.Fatalf("readManagedFiles: %v", err)
				}
				if got := hashArtifactFiles(files); got != writerHash {
					t.Errorf("readManagedFiles hashed %q, writer recorded %q", got, writerHash)
				}
			})
		}
	}
}

// Without the lockfile field, a skill's executable helper is unrecoverable where
// the filesystem has no execute bit — which is what makes the field load-bearing
// rather than belt-and-braces. This asserts the failure the field prevents, so
// nobody removes it as redundant.
func TestWithoutExecutablePathsASkillHelperDrifts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "check.sh"), []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writerHash := hashArtifactFiles([]ArtifactFile{
		{Path: "SKILL.md", Content: []byte("body\n")},
		{Path: "check.sh", Content: []byte("exit 0\n"), Executable: true},
	})

	withExecBitSupport(t, false)
	blind := ManagedFile{Kind: "skill", Name: "fixture"}
	if got, err := contentHashDirManaged(blind, dir); err != nil {
		t.Fatal(err)
	} else if got == writerHash {
		t.Skip("the executable flag happens not to matter for this fixture; nothing is being asserted")
	}
	recorded := ManagedFile{Kind: "skill", Name: "fixture", ExecutablePaths: []string{"check.sh"}}
	got, err := contentHashDirManaged(recorded, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != writerHash {
		t.Errorf("with executable_paths recorded: %q, want %q", got, writerHash)
	}
}
