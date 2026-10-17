package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// RealignSettings brings the KB's settings.yaml up to date (D903): realign
// rewrites its bytes (the removed keys dropped, the schema's comments written;
// config.RealignKBSettings, which kb cannot import), and the result is committed
// as one commit of its own, through the same commit and push as any write. The
// commit is the backup: reverting it restores the file as it was.
//
// It does nothing, and says so with an empty report, when the KB is not a git
// repository (nothing would be reviewable), has no settings.yaml, holds one
// that is not a YAML mapping, already is in the realigned form, or has local
// edits to it that are not committed yet (committing would take a person's
// pending change along). reason goes into the commit's Reason trailer.
func (k *KB) RealignSettings(realign func([]byte) ([]byte, settingschema.Report, error), reason string) (rep settingschema.Report, sha string, err error) {
	if !gitx.IsRepo(k.Root) {
		return rep, "", nil
	}
	err = k.WithGitLock(func() error {
		if err := k.checkRebaseState(); err != nil {
			return err
		}
		path := filepath.Join(k.Root, SettingsFile)
		fi, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: not a regular file (D148)", SettingsFile)
		}
		if out, serr := gitx.StatusPaths(k.Root, SettingsFile); serr == nil && strings.TrimSpace(out) != "" {
			return fmt.Errorf("%s has uncommitted changes: left as it is", SettingsFile)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, r, err := realign(data)
		if err != nil || r.Empty() {
			return err
		}
		if err := writeFileAtomic(path, out); err != nil {
			return err
		}
		msg := "settings: realign settings.yaml\n\nReason: " + reason
		if err := k.commitPathsLocked([]string{SettingsFile}, msg); err != nil {
			_ = writeFileAtomic(path, data) // the commit failed: the file is as it was
			return err
		}
		rep = r
		sha, _ = gitx.HeadSHA(k.Root)
		// Pushed like any write: in the background when the debounce is on,
		// inline otherwise. A failure leaves the commit safe locally.
		if _, ok := k.hasRemote(); ok {
			if k.SyncOutDebounce > 0 {
				k.SchedulePush()
				k.MarkPushPending()
			} else if serr := k.SyncOut(); serr != nil {
				fmt.Fprintf(os.Stderr, "cartographer: push of the settings.yaml realignment: %v\n", serr)
			}
		}
		return nil
	})
	return rep, sha, err
}
