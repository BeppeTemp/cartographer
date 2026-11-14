package service

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// proxyEnvNames is the whole list of shell variables `service install` records
// for the service (#1966): the proxy and the internal CA a user sets in the
// shell for the client and for git. A service starts from a minimal
// environment, so without them the background server fails behind a proxy that
// every command in a terminal goes through. The list is explicit and closed:
// copying the shell's environment would bake a user's whole session, tokens
// included, into a file on disk.
var proxyEnvNames = []string{
	"HTTPS_PROXY", "https_proxy",
	"HTTP_PROXY", "http_proxy",
	"NO_PROXY", "no_proxy",
	"SSL_CERT_FILE", "ssl_cert_file",
}

// EnvVar is one environment variable of a service definition.
type EnvVar struct{ Name, Value string }

// envFileName is the file beside the server config that holds the recorded
// variables. `serve` derives its path from the config (EnvFilePath) and reads it
// by itself: no definition names it on a command line (#2094), because a flag
// the previous stable does not know makes a downgraded service exit 2.
//
// SupervisedEnv is the one line of it that is not a proxy variable: the Windows
// task cannot carry an environment, so the marker that a task restarts the
// process travels in this file.
const (
	envFileName   = "service.env"
	SupervisedEnv = "CARTOGRAPHER_SUPERVISED"
)

// IsProxyEnvName reports whether name is one of the variables the service
// records; `serve --env-file` honours no other.
func IsProxyEnvName(name string) bool {
	for _, n := range proxyEnvNames {
		if n == name {
			return true
		}
	}
	return false
}

// ProxyEnv picks the listed variables out of environ (the "NAME=value" form of
// os.Environ), in the order of the list. An unset or empty variable is absent,
// and so is a value with a line break or a NUL, which the env file cannot carry.
func ProxyEnv(environ []string) []EnvVar {
	last := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && IsProxyEnvName(k) {
			last[k] = v
		}
	}
	var out []EnvVar
	for _, n := range proxyEnvNames {
		if v := last[n]; v != "" && !strings.ContainsAny(v, "\n\r\x00") {
			out = append(out, EnvVar{n, v})
		}
	}
	return out
}

// EnvFilePath is where the recorded variables of the service running
// configPath live: `service.env` in the config's directory.
func EnvFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), envFileName)
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseEnvFile reads `NAME=value` lines (the value is the rest of the line,
// verbatim). Only the listed names are returned; any other line, blank and
// `#` comments aside, is reported in ignored by its key when that is a plain
// identifier, else by its line number: never a value, and never the text of a
// malformed line, which may itself be part of a secret.
func ParseEnvFile(data []byte) (env []EnvVar, ignored []string) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		switch {
		case !ok:
			ignored = append(ignored, fmt.Sprintf("line %d", n))
		case k == SupervisedEnv && v != "":
			env = append(env, EnvVar{k, v})
		case !IsProxyEnvName(k) || v == "":
			if !envKeyRe.MatchString(k) {
				k = fmt.Sprintf("line %d", n)
			}
			ignored = append(ignored, k)
		default:
			env = append(env, EnvVar{k, v})
		}
	}
	return env, ignored
}

// readEnvFile is ParseEnvFile on the env file of configPath; a missing or
// unreadable file records nothing.
func readEnvFile(configPath string) []EnvVar {
	data, err := os.ReadFile(EnvFilePath(configPath))
	if err != nil {
		return nil
	}
	parsed, _ := ParseEnvFile(data)
	var env []EnvVar
	for _, e := range parsed {
		if IsProxyEnvName(e.Name) { // the marker is rendered by the install, not recorded
			env = append(env, e)
		}
	}
	return env
}

// EnvFileNames returns the names the env file of configPath records, in list
// order, never the values: a proxy URL may carry credentials and status is
// printed and pasted.
func EnvFileNames(configPath string) []string {
	var out []string
	for _, e := range readEnvFile(configPath) {
		out = append(out, e.Name)
	}
	return out
}

// recordEnv decides what the service records and writes the env file: the
// variables of this process's environment, or with keep the ones already
// recorded (a binary switch or repair never changes them). With none, a file
// left by an earlier install is removed. The file is written before any
// definition, so a definition never names a file that is not there.
func recordEnv(configPath string, keep bool) ([]EnvVar, error) {
	var env []EnvVar
	if keep {
		env = readEnvFile(configPath)
	} else {
		env = ProxyEnv(environ())
	}
	path := EnvFilePath(configPath)
	base, err := userHomeDir()
	if err != nil {
		return nil, fmt.Errorf("service: resolve home: %w", err)
	}
	// On Windows the file also carries the supervision marker, so it exists
	// whenever the task does.
	marker := goos == "windows"
	if len(env) == 0 && !marker {
		if err := removeSafe(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("service: remove env file: %w", err)
		}
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("# Written by `cartographer service install`; read by `serve`.\n")
	if marker {
		b.WriteString(SupervisedEnv + "=1\n")
	}
	for _, e := range env {
		b.WriteString(e.Name + "=" + e.Value + "\n")
	}
	if err := mkdirSafe(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("service: create env file dir: %w", err)
	}
	// The atomic write gives its temp file the mode of the file it replaces, so a
	// 0644 file (or the target of a link the user listed) is tightened first:
	// the new content is never readable by others, not even before the rename.
	if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("service: env file mode: %w", err)
	}
	if err := safewrite.WriteFileAtomicNoFollow(base, path, []byte(b.String()), 0o600); err != nil {
		return nil, fmt.Errorf("service: write env file: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("service: env file mode: %w", err)
		}
	}
	return env, nil
}

// writePrivateDefinition writes a plist or unit. One that carries recorded variables
// holds credentials a proxy URL may contain, so it is replaced rather than
// overwritten (an existing file keeps its mode) and created 0600.
func writePrivateDefinition(path string, data []byte, private bool) error {
	if !private {
		return writeSafe(path, data)
	}
	base, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	if err := removeSafe(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return safewrite.WriteFile(base, path, data, 0o600)
}
