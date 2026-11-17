package main

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// writeDenial is a provider the sync could not write for lack of permission
// (EPERM or EACCES, a sharing violation on Windows, #2242): a refusal like the
// refused prune, not a failure that stops the other providers.
type writeDenial struct {
	Provider string
	Path     string
	Err      error
}

// isWriteDenied reports a permission error of a write. Every other error keeps
// failing the pass as it did.
func isWriteDenied(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrPermission) || isSharingViolation(err))
}

// newWriteDenial names the path the error carries, the base dir when it has
// none.
func newWriteDenial(provider, baseDir string, err error) writeDenial {
	path := baseDir
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Path != "" {
		path = pe.Path
	}
	return writeDenial{Provider: provider, Path: path, Err: err}
}

// String is the line `sync` prints for it: the provider, the path and the remedy.
func (d writeDenial) String() string {
	return fmt.Sprintf("%s: %s (restore write access to %s)", d.Provider, d.Err, d.Path)
}

func denialLines(ds []writeDenial) []string {
	lines := make([]string, 0, len(ds))
	for _, d := range ds {
		lines = append(lines, d.String())
	}
	return lines
}

// writeDeniedSummary is the error line of the exit-3 report.
func writeDeniedSummary(lines []string) string {
	return "sync could not write these providers, which are NOT synced (the others were; rerun after restoring access): " + escapeControl(strings.Join(lines, "; "))
}
