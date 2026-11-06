package provisioning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Kiro adds these two keys to the file it upgrades; every other top-level key is
// the original's, unchanged. `permissions` is derived by Kiro from the V2 keys
// and cannot be recomputed here: it is the one residual, exploitable only by
// someone who can already write the agents directory and the lockfile.
var kiroDerivedKeys = map[string]bool{"kiroUpgrade": true, "permissions": true}

// Bounds on what is read as a backup: a backup is an agent file, and the
// directory is not ours.
const (
	kiroBackupMaxBytes    = 1 << 20
	kiroBackupMaxNumbered = 16
)

// kiroUpgradeAccepted reports whether the Kiro agent file at full, holding
// data, is one that Kiro CLI 3.x rewrote in place from the bytes Cartographer
// wrote (#1717).
//
// Kiro's session-start auto-upgrade turns a V2 agent into the universal format,
// adds a top-level `kiroUpgrade` block and keeps the original next to it as
// `<file>.bak` (probe 2026-11-06, Kiro CLI 3.0.0: docs/harnesses.md). Read as
// drift, that file would be written back as V2 by the next sync and upgraded
// again by the next session, forever. The file's own bytes are bound, so that a
// later edit is drift again:
//
//   - once the lock holds UpgradedHash, the file is accepted only while it
//     hashes to it;
//   - before that, the first acceptance: the file carries a `kiroUpgrade`
//     object, one of its backups (`.bak`, `.bak.<n>`; regular files, no link)
//     hashes to mf.MaterializedHash, and every top-level key of the file but
//     `permissions` and `kiroUpgrade` equals the backup's (parsed values; a key
//     added or removed is a difference). Sync records the hash then
//     (recordKiroUpgrade).
//
// The block's own `source` fingerprint is not used: its algorithm is Kiro's and
// not documented, and the `.bak` is the original itself.
//
// Read-only: it never writes, and it never follows a link for the `.bak`.
func kiroUpgradeAccepted(mf ManagedFile, full string, data []byte) bool {
	if mf.UpgradedHash != "" {
		return agentBytesHash(mf.Name, data) == mf.UpgradedHash
	}
	var doc struct {
		Upgrade json.RawMessage `json:"kiroUpgrade"`
	}
	if json.Unmarshal(data, &doc) != nil || len(doc.Upgrade) == 0 || doc.Upgrade[0] != '{' {
		return false
	}
	for _, bak := range kiroBackups(full) {
		orig, err := os.ReadFile(bak)
		if err == nil && agentBytesHash(mf.Name, orig) == mf.MaterializedHash && kiroKeysEqual(orig, data) {
			return true
		}
	}
	return false
}

// kiroBackups lists the regular files Kiro keeps the original of full in:
// `<file>.bak`, and `<file>.bak.1`, `.bak.2`... for each further upgrade, since
// Kiro never overwrites an existing backup (probe 2026-11-06, docs/harnesses.md).
// A link is not listed, nor a file over kiroBackupMaxBytes, and at most
// kiroBackupMaxNumbered numbered ones are. Read-only.
func kiroBackups(full string) []string {
	dir, base := filepath.Split(full)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	numbered := 0
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, base+".bak") {
			continue
		}
		suffix := strings.TrimPrefix(n, base+".bak")
		if suffix != "" {
			if suffix[0] != '.' || len(suffix) == 1 || strings.Trim(suffix[1:], "0123456789") != "" {
				continue
			}
			if numbered >= kiroBackupMaxNumbered {
				continue
			}
		}
		if fi, err := e.Info(); err != nil || fi.Size() > kiroBackupMaxBytes {
			continue
		}
		if suffix != "" {
			numbered++
		}
		out = append(out, filepath.Join(dir, n))
	}
	return out
}

// kiroKeysEqual reports whether the two JSON documents carry the same top-level
// keys with the same parsed values, except the keys Kiro derives. A key added or
// removed is a difference (an added allowedTools or mcpServers widens the agent).
func kiroKeysEqual(orig, upgraded []byte) bool {
	var a, b map[string]any
	if json.Unmarshal(orig, &a) != nil || json.Unmarshal(upgraded, &b) != nil {
		return false
	}
	for k, av := range a {
		if kiroDerivedKeys[k] {
			continue
		}
		if bv, ok := b[k]; !ok || !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok && !kiroDerivedKeys[k] {
			return false
		}
	}
	return true
}

func agentBytesHash(name string, data []byte) string {
	return hashArtifactFiles([]ArtifactFile{{Path: agentHashPath(name), Content: data}})
}

// recordKiroUpgrade is the sync side of kiroUpgradeAccepted: for a Kiro agent
// entry without UpgradedHash whose file is an accepted upgrade, it returns the
// entry with the file's hash recorded, so that later edits are drift. Any other
// entry comes back unchanged.
func recordKiroUpgrade(mf ManagedFile, full string) ManagedFile {
	if mf.UpgradedHash != "" || mf.Kind != "agent" {
		return mf
	}
	fi, err := os.Lstat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return mf
	}
	data, err := os.ReadFile(full)
	if err != nil || !kiroUpgradeAccepted(mf, full, data) {
		return mf
	}
	mf.UpgradedHash = agentBytesHash(mf.Name, data)
	return mf
}
