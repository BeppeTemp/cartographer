package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// #2221: a sync that fails after the command migrated .cartographer.yaml does
// not claim the file is untouched: it says it was migrated and names the backup.
func TestSyncErrorText_SaysTheFileWasMigrated(t *testing.T) {
	dir := t.TempDir()
	err := fmt.Errorf("%w (no configuration was modified)", errors.New("manifest pull failed"))
	if got := syncErrorText(err, dir, ""); got != err.Error() {
		t.Errorf("without a migration the text is unchanged, got %q", got)
	}
	backup := clientconfig.Path(dir) + ".bak-v1.2.3"
	got := syncErrorText(err, dir, backup)
	for _, want := range []string{"manifest pull failed", "migrated", backup, clientconfig.Path(dir)} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "no configuration was modified") {
		t.Errorf("the false claim stays: %q", got)
	}
}
