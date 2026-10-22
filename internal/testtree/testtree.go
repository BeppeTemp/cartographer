// Package testtree snapshots a directory tree in memory and restores it
// elsewhere. A test fixture that is expensive to build (a git repository takes
// a dozen processes) is built once and copied per test.
//
// It is test support: production code never imports it (a test in
// internal/repodocs enforces that), and it imports neither internal/kb nor
// the testing package, so the tests of internal/kb can use it too.
package testtree

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Entry is one directory or regular file of a Tree.
type Entry struct {
	// Path is relative to the captured root, slash-separated.
	Path string
	// Mode carries the permission bits only.
	Mode fs.FileMode
	// Dir is true for a directory; Data is then nil.
	Dir  bool
	Data []byte
}

// Tree is a directory tree, parents before their children.
type Tree []Entry

// Capture reads every directory and regular file under dir. Anything else
// (symlink, socket, device) is an error naming its path: a snapshot that
// silently dropped it would restore a different tree.
func Capture(dir string) (Tree, error) {
	var tree Tree
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			tree = append(tree, Entry{Path: rel, Mode: info.Mode().Perm(), Dir: true})
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			tree = append(tree, Entry{Path: rel, Mode: info.Mode().Perm(), Data: data})
		default:
			return fmt.Errorf("testtree: %s is neither a directory nor a regular file (%s)", rel, info.Mode().Type())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// Restore creates dir and writes the tree into it, preserving the permission
// bits.
func (tr Tree) Restore(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var dirs []Entry
	for _, e := range tr {
		p := filepath.Join(dir, filepath.FromSlash(e.Path))
		if e.Dir {
			// Created writable so the children can be written; the recorded
			// mode is applied once the whole tree is in place.
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, e)
			continue
		}
		if err := os.WriteFile(p, e.Data, e.Mode); err != nil {
			return err
		}
		// WriteFile's mode is filtered by the umask.
		if err := os.Chmod(p, e.Mode); err != nil {
			return err
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return len(dirs[i].Path) > len(dirs[j].Path) })
	for _, e := range dirs {
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(e.Path)), e.Mode); err != nil {
			return err
		}
	}
	return nil
}
