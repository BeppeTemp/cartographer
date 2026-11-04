package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelPathErr_ShowsPathsBelowTheRootRelativeToIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kb")
	other := filepath.Join(t.TempDir(), "elsewhere", "f.txt")
	err := fmt.Errorf("step: %w", &os.LinkError{Op: "rename", Old: filepath.Join(root, "m", "x"), New: filepath.Join(root, "m", "y"), Err: os.ErrExist})
	got := relPathErr(err, root)
	if strings.Contains(got, root) {
		t.Fatalf("root left in %q", got)
	}
	if want := "step: rename m/x m/y: file already exists"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = relPathErr(&os.PathError{Op: "open", Path: other, Err: os.ErrNotExist}, root)
	if want := "open f.txt: file does not exist"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := relPathErr(errors.New("plain "+root+"/a/b"), root); got != "plain a/b" {
		t.Fatalf("plain text: %q", got)
	}
}

func TestConceptMove_FilesystemErrorsCarryNoHostPaths(t *testing.T) {
	k, s := d160Fixture(t)
	d160Write(t, k, "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	d160Write(t, k, "n/_map.md", "---\ntype: Map\nkind: map\ntitle: N\n---\n# N\n")
	d160Write(t, k, "m/c.md", "---\ntype: Note\ntitle: C\n---\n# C\n")
	if err := k.ExpandConcept("m/c"); err != nil {
		t.Fatal(err)
	}
	orig := conceptMoveRename
	t.Cleanup(func() { conceptMoveRename = orig })
	conceptMoveRename = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrExist}
	}
	args, _ := json.Marshal(map[string]string{"source_id": "m/c", "target_id": "n/c"})
	res, err := s.Tools()["concept_move"].Handler(authLocalContext(), args)
	if err != nil || !res.IsError {
		t.Fatalf("want an error result: %+v err=%v", res, err)
	}
	msg := res.Content[0].Text
	for _, bad := range []string{k.Root, filepath.Dir(k.Root)} {
		if strings.Contains(msg, bad) {
			t.Fatalf("message %q carries %q", msg, bad)
		}
	}
	if !strings.Contains(msg, "rename") {
		t.Fatalf("message lost the cause: %q", msg)
	}
}
