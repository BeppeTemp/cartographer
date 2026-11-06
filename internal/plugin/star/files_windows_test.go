//go:build windows

package star

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"golang.org/x/sys/windows"
)

// The Windows anchors resolve below home and their cartographer directories
// stay denied.
func TestFilesWindowsAnchors(t *testing.T) {
	e := newFileEnv(t, []string{"{app_data}/**", "{local_app_data}/**"}, []string{"{app_data}/**"})
	roaming, local := filepath.Join(e.home, "AppData", "Roaming"), filepath.Join(e.home, "AppData", "Local")
	e.perms = &plugin.Permissions{Files: e.perms.Files}
	run := func(body string) error {
		t.Setenv("APPDATA", roaming)
		t.Setenv("LOCALAPPDATA", local)
		_, err := e.runEnv(t, body, map[string]string{"APPDATA": roaming, "LOCALAPPDATA": local})
		return err
	}
	if err := run(`files.write("{app_data}/tool/a.txt", "x")`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"{app_data}/cartographer/server.yaml", "{local_app_data}/Cartographer/x"} {
		callErr(t, run(`files.read("`+p+`")`), Denied)
	}
	if err := run(`files.read("{documents}/x")`); err == nil || strings.Contains(err.Error(), "panic") {
		t.Errorf("documents without a grant: %v", err)
	}
}

// An 8.3 short spelling of a denied directory is the same directory: it is
// denied after resolution, whatever the lexical check saw.
func TestFilesShortNamesOfDeniedDirs(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/*/*"}, []string{"{home}/*/*"})
	long := filepath.Join(e.home, "cartographer-data")
	e.put(t, "cartographer-data/secret.txt", "s")
	src, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(src, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("no short name: %v", err)
	}
	short := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(short, "cartographer-data") {
		t.Skip("8.3 names are disabled on this volume")
	}
	for _, call := range []string{
		fmt.Sprintf(`files.read("{home}/%s/secret.txt")`, short),
		fmt.Sprintf(`files.write("{home}/%s/secret.txt", "x")`, short),
		fmt.Sprintf(`files.write("{home}/%s/new.txt", "x")`, short),
	} {
		callErr(t, mustErr(e.run(t, call)), Denied)
	}
	if b, _ := os.ReadFile(filepath.Join(long, "secret.txt")); string(b) != "s" {
		t.Errorf("file changed: %q", b)
	}
}
