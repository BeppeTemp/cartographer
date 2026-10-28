package configurator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// CreatedLedgerName is the small record, in a client base dir, of the config
// files Cartographer itself created there (#1269). A Claude Code config or
// settings file is deleted once it is `{}` only when it is listed in it: a user's
// own `{}` file is reduced and kept. The ledger is deleted when its last entry
// is, so a connect/disconnect round trip on an empty HOME leaves no trace of it.
const CreatedLedgerName = ".cartographer-created.json"

type createdLedger struct {
	Created []string `json:"created"`
}

func readCreated(baseDir string) createdLedger {
	var l createdLedger
	data, err := os.ReadFile(filepath.Join(baseDir, CreatedLedgerName))
	if err == nil {
		_ = json.Unmarshal(data, &l)
	}
	return l
}

func writeCreated(baseDir string, l createdLedger) error {
	path := filepath.Join(baseDir, CreatedLedgerName)
	if len(l.Created) == 0 {
		if err := safewrite.Remove(baseDir, path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	sort.Strings(l.Created)
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return safewrite.WriteFile(baseDir, path, append(data, '\n'), 0o644)
}

// WasCreated reports whether rel (relative to baseDir) is a file Cartographer
// created, per the ledger.
func WasCreated(baseDir, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range readCreated(baseDir).Created {
		if p == rel {
			return true
		}
	}
	return false
}

// RecordCreated notes that Cartographer created rel (relative to baseDir).
func RecordCreated(baseDir, rel string) error {
	if WasCreated(baseDir, rel) {
		return nil
	}
	l := readCreated(baseDir)
	l.Created = append(l.Created, filepath.ToSlash(rel))
	return writeCreated(baseDir, l)
}

// ForgetCreated drops rel from the ledger, deleting the ledger when empty.
func ForgetCreated(baseDir, rel string) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	kept := l.Created[:0]
	for _, p := range l.Created {
		if p != rel {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(l.Created) {
		return nil
	}
	l.Created = kept
	return writeCreated(baseDir, l)
}

// MayDeleteEmpty reports whether the config file rel of provider, left empty,
// may be deleted: DeletableWhenEmpty, and for a provider whose file is shared
// state (DeleteOnlyIfCreated) only when Cartographer created it.
func MayDeleteEmpty(d Descriptor, baseDir, rel string) bool {
	if !d.DeletableWhenEmpty {
		return false
	}
	return !d.DeleteOnlyIfCreated || WasCreated(baseDir, rel)
}
