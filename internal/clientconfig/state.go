package clientconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StateFileName is the machine-written cache next to .cartographer.yaml (D747),
// in the same directory as the client lock files. It holds what the server
// advertised at the last connect or sync, so that a sync that only refreshes
// the cache never touches the file a person edits. Never hand-edited.
const StateFileName = ".cartographer-state.json"

// stateFile is the JSON shape of StateFileName: the server-owned caches and, since
// D906, the two lists the client's own commands maintain (agents, the sync-timer
// opt-out), which a person never edits.
type stateFile struct {
	// KnownKBs is the KB names advertised at the last successful connect/sync.
	KnownKBs []string `json:"known_kbs"`
	// ServerMountMode and ServerRoutedPath record the mount topology (D187).
	ServerMountMode  string `json:"server_mount_mode,omitempty"`
	ServerRoutedPath string `json:"server_routed_path,omitempty"`
	// Agents are the connected provider names. A pointer, so an absent key (a
	// state file from before D906: read the YAML's list) is told from an empty
	// list (every client disconnected).
	Agents *[]string `json:"agents,omitempty"`
	// SyncTimerOptOut is the operator's `service sync-timer uninstall` (D325).
	SyncTimerOptOut bool `json:"sync_timer_opt_out,omitempty"`
}

// StatePath returns the full path to the state file inside dir.
func StatePath(dir string) string { return filepath.Join(dir, StateFileName) }

// loadState reads the state file. ok is false when it is absent or cannot be
// read or parsed: it is a cache, so the caller falls back to the legacy keys of
// the YAML file and the next sync rebuilds it. A corrupt file is reported once
// on stderr rather than failing the command.
func loadState(dir string) (s stateFile, ok bool) {
	data, err := os.ReadFile(StatePath(dir))
	if err == nil {
		if err = json.Unmarshal(data, &s); err == nil {
			return s, true
		}
	}
	if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "clientconfig: ignoring %s (%v); the next sync rebuilds it\n", StatePath(dir), err)
	}
	return stateFile{}, false
}

// writeState writes the state file when it differs from what is on disk. A
// machine that has never synced, and has no state file yet, gets none.
func writeState(dir string, cfg *Config) error {
	_, err := writeStateChanged(dir, cfg)
	return err
}

// writeStateChanged is writeState and says whether it wrote anything.
func writeStateChanged(dir string, cfg *Config) (bool, error) {
	s := stateFile{KnownKBs: cfg.KnownKBs, ServerMountMode: cfg.ServerMountMode, ServerRoutedPath: cfg.ServerRoutedPath, SyncTimerOptOut: cfg.SyncTimerOptOut}
	if cfg.Agents != nil {
		agents := cfg.Agents
		s.Agents = &agents
	}
	if s.KnownKBs == nil {
		s.KnownKBs = []string{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, fmt.Errorf("clientconfig: marshal state: %w", err)
	}
	data = append(data, '\n')
	old, readErr := os.ReadFile(StatePath(dir))
	if readErr != nil && len(s.KnownKBs) == 0 && s.ServerMountMode == "" && s.ServerRoutedPath == "" && s.Agents == nil && !s.SyncTimerOptOut {
		return false, nil
	}
	if bytes.Equal(old, data) {
		return false, nil
	}
	return true, writeAtomic(dir, StateFileName, data)
}

// writeAtomic replaces dir/name with data through a temp file and a rename.
func writeAtomic(dir, name string, data []byte) error {
	return writeAtomicMode(dir, name, data, 0o644)
}

// writeAtomicMode is writeAtomic with the file mode of the result.
func writeAtomicMode(dir, name string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("clientconfig: write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("clientconfig: chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("clientconfig: close temp: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("clientconfig: replace %s: %w", filepath.Join(dir, name), err)
	}
	return nil
}
