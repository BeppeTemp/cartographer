package service

// The Windows side of a server hosted in WSL: a marker file that says "the
// configured server is the one in this distro", and the wsl.exe calls that
// act on it. Not build-tagged, so the tests run everywhere with a fake run;
// only cartographer.exe calls it.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// ErrNoWsl is returned when wsl.exe is not on this machine.
var ErrNoWsl = errors.New("wsl.exe not found: WSL is not installed")

// WslMarkerPath is %LOCALAPPDATA%\cartographer\wsl-server.json, beside the
// task definitions.
func WslMarkerPath() (string, error) {
	dir, err := localAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cartographer", "wsl-server.json"), nil
}

type wslMarker struct {
	Distro string `json:"distro"`
}

// ReadWslMarker returns the distro the marker names, "" when there is no
// marker. A marker that does not parse, or names no valid distro, is an error:
// guessing would act on the wrong server.
func ReadWslMarker() (string, error) {
	p, err := WslMarkerPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var m wslMarker
	if err := json.Unmarshal(data, &m); err != nil || !wslDistroPattern.MatchString(m.Distro) {
		return "", fmt.Errorf("service: %s does not name a WSL distro; delete it or run `cartographer service install --wsl <distro>`", p)
	}
	return m.Distro, nil
}

// WriteWslMarker records distro as the host of the configured server.
func WriteWslMarker(distro string) error {
	if !wslDistroPattern.MatchString(distro) {
		return fmt.Errorf("service: %q is not a WSL distro name", distro)
	}
	p, err := WslMarkerPath()
	if err != nil {
		return err
	}
	if err := mkdirSafe(filepath.Dir(p)); err != nil {
		return err
	}
	data, _ := json.Marshal(wslMarker{Distro: distro})
	return writeSafe(p, append(data, '\n'))
}

// RemoveWslMarker removes the marker; an absent one is success.
func RemoveWslMarker() error {
	p, err := WslMarkerPath()
	if err != nil {
		return err
	}
	if err := removeSafe(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ValidWslDistro reports whether name can be passed to `wsl.exe -d`.
func ValidWslDistro(name string) bool { return wslDistroPattern.MatchString(name) }

// wslAttached runs a command with this process's stdio, so the user sees the
// Linux side's own output, and returns its exit code. A test seam.
var wslAttached = func(name string, args ...string) (int, error) {
	if err := guardScheduler(name); err != nil {
		return 0, err
	}
	cmd := procattr.Hide(exec.Command(name, args...))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ex *exec.ExitError
	if errors.As(err, &ex) {
		return ex.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func (m *Manager) wslExe() error {
	if _, err := lookPath("wsl.exe"); err != nil {
		return ErrNoWsl
	}
	return nil
}

// DelegateToWsl runs `wsl.exe -d <distro> -- cartographer service <args...>`
// with stdio attached and returns the child's exit code, which is the exit
// code cartographer.exe reports.
func (m *Manager) DelegateToWsl(distro string, args []string) (int, error) {
	if err := m.wslExe(); err != nil {
		return 0, err
	}
	if !ValidWslDistro(distro) {
		return 0, fmt.Errorf("service: %q is not a WSL distro name", distro)
	}
	return wslAttached("wsl.exe", append([]string{"-d", distro, "--", "cartographer", "service"}, args...)...)
}

// WslCartographerVersion is the output of `cartographer version` inside the
// distro; an error means the distro has no usable cartographer.
func (m *Manager) WslCartographerVersion(distro string) (string, error) {
	if err := m.wslExe(); err != nil {
		return "", err
	}
	if !ValidWslDistro(distro) {
		return "", fmt.Errorf("service: %q is not a WSL distro name", distro)
	}
	out, err := m.stdout("wsl.exe", "-d", distro, "--", "cartographer", "version")
	return strings.TrimSpace(out), err
}

// WslServerRuns reports, as nil, that `cartographer service status` inside
// distro exits 0, i.e. that distro's service is the server that answers; any
// other outcome is the error, so a caller can say why (#2093).
func (m *Manager) WslServerRuns(distro string) error {
	if err := m.wslExe(); err != nil {
		return err
	}
	if !ValidWslDistro(distro) {
		return fmt.Errorf("service: %q is not a WSL distro name", distro)
	}
	_, err := m.stdout("wsl.exe", "-d", distro, "--", "cartographer", "service", "status")
	return err
}

// ServeTaskRegistered reports whether the native `\Cartographer\Serve` task
// is registered: with a WSL server too, two servers would want one port.
func (m *Manager) ServeTaskRegistered() bool {
	return m.windowsTaskRegistered(windowsServeTaskName)
}

// ListWslDistros lists the installed distros (`wsl.exe -l -q`), only the
// running ones when running is true; a name wsl -d could not take is dropped.
func (m *Manager) ListWslDistros(running bool) ([]string, error) {
	if err := m.wslExe(); err != nil {
		return nil, err
	}
	args := []string{"-l", "-q"}
	if running {
		args = append(args, "--running")
	}
	out, err := m.stdout("wsl.exe", args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(decodeWslOutput([]byte(out)), "\n") {
		if l = strings.TrimSpace(l); ValidWslDistro(l) {
			names = append(names, l)
		}
	}
	return names, nil
}

// decodeWslOutput decodes wsl.exe's own output, which is UTF-16LE (BOM
// optional) whatever the console code page; output that is not (WSL_UTF8=1)
// is returned as is. NUL padding and CR are dropped.
func decodeWslOutput(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	} else if len(b) < 2 || b[1] != 0 {
		return strings.NewReplacer("\x00", "", "\r", "").Replace(string(b))
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return strings.NewReplacer("\x00", "", "\r", "").Replace(string(utf16.Decode(u)))
}
