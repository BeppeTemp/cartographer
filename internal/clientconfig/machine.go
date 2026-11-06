package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// MachineFileName is the durable machine-written file next to .cartographer.yaml
// (D1728): what the client's own commands record and no sync can
// rebuild, namely the local approvals of hooks, executable artifacts and MCP
// descriptors, the marker that the pre-approval installs were migrated, and the
// operator's opt-outs (sync timer, tray). Never hand-edited.
//
// It is not the state file, which is a cache tolerated when corrupt: here an
// unreadable file fails closed. Load grants no approval, reports it, and Save
// refuses to replace it with what only this load knew (see machineBroken).
const MachineFileName = ".cartographer-machine.json"

// machineFile is the JSON shape of MachineFileName.
type machineFile struct {
	Approvals         map[string]map[string]MCPApproval `json:"approvals,omitempty"`
	ApprovalsMigrated bool                              `json:"approvals_migrated,omitempty"`
	SyncTimerOptOut   bool                              `json:"sync_timer_opt_out,omitempty"`
	TrayOptOut        bool                              `json:"tray_opt_out,omitempty"`
}

// MachinePath returns the full path to the machine file inside dir.
func MachinePath(dir string) string { return filepath.Join(dir, MachineFileName) }

// legacyMachineKeys finds the keys of the old homes of the machine file's data
// in the bytes of the YAML or of the state file, to decide on a backup.
var legacyMachineKeys = regexp.MustCompile(`(?m)^(mcp_approvals|sync_timer_opt_out):`)

// readMachine reads the machine file. exists is false only when the file is
// absent; err is set when it exists and cannot be read or parsed.
func readMachine(dir string) (m machineFile, exists bool, err error) {
	data, err := os.ReadFile(MachinePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return m, false, nil
	}
	if err != nil {
		return m, true, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return machineFile{}, true, err
	}
	return m, true, nil
}

// loadMachine fills the machine-owned fields of cfg. yamlMigrated is the
// approvals_migrated key of the YAML, which stays there as a tombstone after the
// migration, and yamlApprovals says the YAML still holds mcp_approvals.
//
// Present: the file is the only source. Absent and the YAML still holds
// mcp_approvals: the pre-split homes are read (the opt-outs from the state file
// or the YAML), and the next Save moves them, with a backup. Absent after a
// migration (the tombstone is set and mcp_approvals is gone), or unreadable:
// fail closed, nothing is approved and nothing is migrated.
func (c *Config) loadMachine(dir string, legacy *legacyCaches, state stateFile, yamlMigrated, yamlApprovals bool) {
	m, exists, err := readMachine(dir)
	switch {
	case err != nil:
		// Nothing is approved. The migration marker is set so the first sync does
		// not approve what is installed in its place, and the opt-outs are the
		// state file's copy, which the machine file's writes keep current. Save
		// keeps the file, and the old keys of the YAML, as they are.
		c.MCPApprovals = nil
		c.ApprovalsMigrated = true
		c.SyncTimerOptOut, c.TrayOptOut = state.SyncTimerOptOut, state.TrayOptOut
		c.machineBase = [2]bool{state.SyncTimerOptOut, state.TrayOptOut}
		c.machineBroken = err
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s is unreadable (%v): no hook, executable artifact or MCP descriptor is approved until it is fixed or removed, and nothing is migrated; once it is removed or rewritten, give the approvals again with `cartographer approve` (the YAML as it was before the migration is %s.bak-pre-machine, when there is one)", MachinePath(dir), err, Path(dir)))
	case exists:
		c.MCPApprovals, c.ApprovalsMigrated = m.Approvals, m.ApprovalsMigrated
		c.SyncTimerOptOut, c.TrayOptOut = m.SyncTimerOptOut, m.TrayOptOut
	case yamlMigrated && !yamlApprovals:
		// The migration ran and the file is gone (deleted, or a dangling link):
		// the marker in the YAML says the pre-approval migration must not run
		// again, and there is no approval to read.
		c.MCPApprovals = nil
		c.ApprovalsMigrated = true
		c.SyncTimerOptOut = state.SyncTimerOptOut || legacy.SyncTimerOptOut
		c.TrayOptOut = state.TrayOptOut
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s is missing: no hook, executable artifact or MCP descriptor is approved and nothing is migrated; give the approvals again with `cartographer approve`", MachinePath(dir)))
	default:
		c.MCPApprovals, c.ApprovalsMigrated = legacy.MCPApprovals, yamlMigrated
		c.SyncTimerOptOut = state.SyncTimerOptOut || legacy.SyncTimerOptOut
		c.TrayOptOut = state.TrayOptOut
	}
}

// writeMachineChanged writes the machine file when it differs from what is on
// disk, and says whether it did. A machine with nothing to record gets no file.
// The first write of a migration keeps the old homes as .bak-pre-machine copies
// before anything is dropped from them.
func writeMachineChanged(dir string, cfg *Config) (bool, error) {
	m := machineFile{Approvals: cfg.MCPApprovals, ApprovalsMigrated: cfg.ApprovalsMigrated, SyncTimerOptOut: cfg.SyncTimerOptOut, TrayOptOut: cfg.TrayOptOut}
	if len(m.Approvals) == 0 {
		m.Approvals = nil
	}
	empty := m.Approvals == nil && !m.ApprovalsMigrated && !m.SyncTimerOptOut && !m.TrayOptOut
	if cfg.machineBroken != nil {
		// Fail closed: never replace a file that could not be read with what this
		// load knew. Only an approval, or an opt-out changed against the state
		// file's copy, refuses the save.
		if len(cfg.MCPApprovals) > 0 || [2]bool{cfg.SyncTimerOptOut, cfg.TrayOptOut} != cfg.machineBase {
			return false, fmt.Errorf("clientconfig: %s is unreadable (%v), not replacing it: fix or remove it, then repeat the command", MachinePath(dir), cfg.machineBroken)
		}
		return false, nil
	}
	old, readErr := os.ReadFile(MachinePath(dir))
	if readErr != nil && empty {
		return false, nil
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, fmt.Errorf("clientconfig: marshal machine file: %w", err)
	}
	data = append(data, '\n')
	if string(old) == string(data) {
		return false, nil
	}
	if readErr != nil {
		if err := backupLegacy(dir, FileName); err != nil {
			return false, err
		}
	}
	return true, writeAtomicMode(dir, MachineFileName, data, 0o600, true)
}

// backupLegacy copies dir/name to name.bak-pre-machine when it still holds one
// of the keys the machine file takes over, and no such backup exists yet. The
// copy is private: the YAML may carry a literal token.
func backupLegacy(dir, name string) error {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || !legacyMachineKeys.Match(data) {
		return nil
	}
	backup := name + ".bak-pre-machine"
	if _, err := os.Stat(filepath.Join(dir, backup)); err == nil {
		return nil
	}
	return writeAtomicMode(dir, backup, data, 0o600, true)
}

// keptLegacyKeys are the dropped YAML keys Save must leave in place: while the
// machine file cannot be read, mcp_approvals and sync_timer_opt_out are all that
// is left of their values, and no backup has been made.
func (c *Config) keptLegacyKeys() map[string]bool {
	if c.machineBroken == nil {
		return nil
	}
	return map[string]bool{"mcp_approvals": true, "sync_timer_opt_out": true}
}
