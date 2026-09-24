package updatecheck

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Channel is how the running binary was installed, which decides the one
// upgrade command that does not leave two Cartographers on PATH (ops skill
// §Upgrade).
type Channel string

const (
	ChannelHomebrew   Channel = "homebrew"
	ChannelInstallSh  Channel = "install.sh"
	ChannelInstallPS1 Channel = "install.ps1"
	ChannelGoInstall  Channel = "go-install"
	ChannelContainer  Channel = "container"
	ChannelUnknown    Channel = "unknown"
)

const (
	installShURL  = "https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh"
	installPS1URL = "https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1"
	brewCask      = "beppetemp/tap/cartographer"
	goModule      = "github.com/BeppeTemp/cartographer/cmd/cartographer"
)

// UpgradeCommand is the command a user runs to upgrade through this channel,
// or "" when none can be named (container, unknown). latest is only used by
// go-install, whose command pins the version.
func (c Channel) UpgradeCommand(latest string) string {
	switch c {
	case ChannelHomebrew:
		return "brew upgrade --cask " + brewCask
	case ChannelInstallSh:
		return "curl -fsSL " + installShURL + " | sh -s -- update"
	case ChannelInstallPS1:
		return "& ([scriptblock]::Create((irm " + installPS1URL + "))) update"
	case ChannelGoInstall:
		if latest == "" {
			latest = "latest"
		}
		return "go install " + goModule + "@" + latest
	}
	return ""
}

// ApplyArgv is the non-interactive argv `update apply` runs for this channel,
// or nil when the channel is never patched automatically (D254: go-install,
// container and unknown only ever get the notice).
func (c Channel) ApplyArgv() []string {
	switch c {
	case ChannelHomebrew:
		return []string{"brew", "upgrade", "--cask", brewCask}
	case ChannelInstallSh:
		return []string{"sh", "-c", c.UpgradeCommand("")}
	case ChannelInstallPS1:
		return []string{"powershell", "-NoProfile", "-Command", c.UpgradeCommand("")}
	}
	return nil
}

// Env is what channel detection reads from the machine, injectable so a test
// can describe any platform from any other.
type Env struct {
	GOOS         string
	Getenv       func(string) string
	HomeDir      string
	EvalSymlinks func(string) (string, error)
	Exists       func(string) bool
}

// SystemEnv is the real machine.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:         runtime.GOOS,
		Getenv:       os.Getenv,
		HomeDir:      home,
		EvalSymlinks: filepath.EvalSymlinks,
		Exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

// DetectChannel classifies exePath (normally os.Executable()) on this
// machine.
func DetectChannel(exePath string) Channel {
	return DetectChannelIn(exePath, SystemEnv())
}

// DetectChannelIn classifies exePath against env. The as-invoked path and its
// symlink-resolved target are both considered: a Homebrew cask is reached
// through /opt/homebrew/bin or /usr/local/bin but lives in the Caskroom, and
// the same /usr/local/bin path is install.sh's default when it is a plain
// file.
func DetectChannelIn(exePath string, env Env) Channel {
	if exePath == "" {
		return ChannelUnknown
	}
	getenv := env.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	resolved := exePath
	if env.EvalSymlinks != nil {
		if r, err := env.EvalSymlinks(exePath); err == nil && r != "" {
			resolved = r
		}
	}
	windows := env.GOOS == "windows"
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\`, "/")
		if windows {
			p = strings.ToLower(p)
		}
		return strings.TrimRight(p, "/")
	}
	dirOf := func(p string) string {
		p = norm(p)
		if i := strings.LastIndex(p, "/"); i >= 0 {
			return p[:i]
		}
		return ""
	}
	join := func(elem ...string) string { return norm(strings.Join(elem, "/")) }
	res := norm(resolved)
	exeDir, resDir := dirOf(exePath), dirOf(resolved)
	inDir := func(dir string) bool {
		if dir == "" {
			return false
		}
		d := norm(dir)
		return exeDir == d || resDir == d
	}

	if windows {
		installDir := getenv("CARTOGRAPHER_INSTALL_DIR")
		if installDir == "" {
			if lad := getenv("LOCALAPPDATA"); lad != "" {
				installDir = join(lad, "Cartographer", "bin")
			}
		}
		if inDir(installDir) {
			return ChannelInstallPS1
		}
		if inDir(goBinDir(getenv, env.HomeDir, windows)) {
			return ChannelGoInstall
		}
		return ChannelUnknown
	}

	if strings.Contains(res, "/Caskroom/") ||
		strings.HasPrefix(res, "/opt/homebrew/") ||
		strings.HasPrefix(res, "/home/linuxbrew/.linuxbrew/") {
		return ChannelHomebrew
	}
	// A container image has one binary and no channel inside it: the upgrade
	// is a new image tag, decided outside.
	if env.Exists != nil && env.Exists("/.dockerenv") {
		return ChannelContainer
	}
	if inDir(goBinDir(getenv, env.HomeDir, windows)) {
		return ChannelGoInstall
	}
	if inDir(getenv("CARTOGRAPHER_INSTALL_DIR")) || inDir("/usr/local/bin") ||
		(env.HomeDir != "" && inDir(join(env.HomeDir, ".local", "bin"))) {
		return ChannelInstallSh
	}
	return ChannelUnknown
}

// goBinDir is where `go install` puts binaries, read from the environment
// only (GOBIN, else the first GOPATH entry's bin, else $HOME/go/bin) — never by
// running `go`.
func goBinDir(getenv func(string) string, home string, windows bool) string {
	if v := getenv("GOBIN"); v != "" {
		return v
	}
	if v := getenv("GOPATH"); v != "" {
		first := v
		sep := ":"
		if windows {
			sep = ";"
		}
		if i := strings.Index(v, sep); i >= 0 {
			first = v[:i]
		}
		if first != "" {
			return strings.TrimRight(strings.ReplaceAll(first, `\`, "/"), "/") + "/bin"
		}
	}
	if home != "" {
		return strings.TrimRight(strings.ReplaceAll(home, `\`, "/"), "/") + "/go/bin"
	}
	return ""
}
