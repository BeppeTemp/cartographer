package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// staleBridgeEntry is a managed bridge entry (D737) whose command is not the
// one `cartographer sync` would write now: the binary it names was moved or
// removed since connect found it on PATH.
type staleBridgeEntry struct {
	Name, Path, Have, Want string
	Missing                bool
}

// staleBridgeEntries compares the command of each bridge entry already on disk
// with the current bridgeCommand(), the one sync writes. Only an entry that is
// itself the bridge (`<command> mcp --provider <provider>`) is judged: one the
// user wrote by hand under the same name, or an HTTP entry, is not ours to call
// stale. A config behind a symlink is skipped, as sync would refuse it (D148).
func staleBridgeEntries(provider string, entries []mcpEntry, dir string, claudeDirs []string) []staleBridgeEntry {
	d, ok := configurator.Lookup(configurator.Provider(provider))
	if !ok || d.MCPConfigPath == "" {
		return nil
	}
	want, err := bridgeCommand()
	if err != nil {
		return nil
	}
	var out []staleBridgeEntry
	for _, e := range entries {
		if !e.Bridge {
			continue
		}
		for _, base := range providerBaseDirs(provider, dir, claudeDirs) {
			fp := filepath.Join(base, d.ConfigPath())
			if safewrite.CheckPath(base, fp) != nil {
				continue
			}
			data, err := os.ReadFile(fp)
			if err != nil {
				continue
			}
			have, ok := bridgeCommandOnDisk(d, data, e.Name, provider)
			if !ok || have == want {
				continue
			}
			_, statErr := os.Stat(have)
			out = append(out, staleBridgeEntry{Name: e.Name, Path: fp, Have: have, Want: want, Missing: os.IsNotExist(statErr)})
		}
	}
	return out
}

var (
	tomlCommandLine = regexp.MustCompile(`(?m)^\s*command\s*=\s*("(?:[^"\\\n]|\\.)*")\s*$`)
	tomlArgsLine    = regexp.MustCompile(`(?m)^\s*args\s*=\s*\[(.*)\]\s*$`)
	tomlQuoted      = regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`)
)

// bridgeCommandOnDisk returns the command of entry name in a provider's config
// file when that entry is the stdio bridge for provider, and false for anything
// else.
func bridgeCommandOnDisk(d configurator.Descriptor, data []byte, name, provider string) (string, bool) {
	var command string
	var args []string
	if d.MCPFormat == configurator.FormatTOMLBlock {
		table, found := configurator.CodexMCPTable(string(data), name)
		if !found {
			return "", false
		}
		m := tomlCommandLine.FindStringSubmatch(table)
		if m == nil {
			return "", false
		}
		command, _ = strconv.Unquote(m[1])
		if a := tomlArgsLine.FindStringSubmatch(table); a != nil {
			for _, q := range tomlQuoted.FindAllString(a[1], -1) {
				s, _ := strconv.Unquote(q)
				args = append(args, s)
			}
		}
	} else {
		var existing map[string]any
		if json.Unmarshal(data, &existing) != nil {
			return "", false
		}
		server, _ := extractMCPServer(existing, name).(map[string]any)
		if server == nil {
			return "", false
		}
		switch c := server["command"].(type) {
		case string:
			command = c
			args = stringList(server["args"])
		case []any:
			// OpenCode keeps the command and its arguments in one array.
			parts := stringList(c)
			if len(parts) == 0 {
				return "", false
			}
			command, args = parts[0], parts[1:]
		default:
			return "", false
		}
	}
	if command == "" || !slices.Equal(args, []string{"mcp", "--provider", provider}) {
		return "", false
	}
	return command, true
}

func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// onDiskBridgeCommand is the command of a bridge entry found in a provider's
// configuration before sync removes it: the fallback of bridgeCommand when
// `cartographer` is not on PATH (a systemd or launchd timer's short PATH).
var onDiskBridgeCommand string

// onDiskBridgeMissing is the first command the entries on disk name that is no
// longer a usable file: the warning of an entry that is kept says it points at
// nothing.
var onDiskBridgeMissing string

// noteOnDiskBridgeCommand records the command of the first bridge entry already
// written for the targeted providers. It runs before removeMCPEntries, which
// deletes the entries it is read from.
func noteOnDiskBridgeCommand(targets []string, entriesByProvider map[string][]mcpEntry, dir string, claudeDirs []string) {
	onDiskBridgeCommand, onDiskBridgeMissing = "", ""
	for _, p := range targets {
		d, ok := configurator.Lookup(configurator.Provider(p))
		if !ok || d.MCPConfigPath == "" {
			continue
		}
		for _, e := range entriesByProvider[p] {
			if !e.Bridge {
				continue
			}
			for _, base := range providerBaseDirs(p, dir, claudeDirs) {
				fp := filepath.Join(base, d.ConfigPath())
				if safewrite.CheckPath(base, fp) != nil {
					continue
				}
				data, err := os.ReadFile(fp)
				if err != nil {
					continue
				}
				if have, ok := bridgeCommandOnDisk(d, data, e.Name, p); ok {
					if usableBridgeFile(have) {
						onDiskBridgeCommand = have
						return
					}
					if onDiskBridgeMissing == "" {
						onDiskBridgeMissing = have
					}
				}
			}
		}
	}
}

// usableOnDiskBridgeCommand is the recorded command when it is still usable.
func usableOnDiskBridgeCommand() string {
	if usableBridgeFile(onDiskBridgeCommand) {
		return onDiskBridgeCommand
	}
	return ""
}

// usableBridgeFile reports whether command is an existing executable regular
// file that is not under a versioned Cellar directory (D737: the next Homebrew
// upgrade deletes it).
func usableBridgeFile(command string) bool {
	if command == "" || !filepath.IsAbs(command) {
		return false
	}
	if slices.Contains(strings.Split(filepath.ToSlash(command), "/"), "Cellar") {
		return false
	}
	info, err := os.Stat(command)
	return err == nil && info.Mode().IsRegular() && (!execbit.Supported || execbit.IsExecutable(info.Mode()))
}

// providersToClear drops from targets the providers whose bridge entry cannot
// be written because the bridge command is unresolvable and there is no HTTP
// entry to fall back to (emitMCPEntry): removing their entries first would
// leave the client without the server, so they are left exactly as they are.
func providersToClear(targets []string, entriesByProvider map[string][]mcpEntry) []string {
	if _, err := bridgeCommand(); err == nil {
		return targets
	}
	out := make([]string, 0, len(targets))
	for _, p := range targets {
		keep := false
		for _, e := range entriesByProvider[p] {
			if e.Bridge && e.URL == "" {
				keep = true
			}
		}
		if !keep {
			out = append(out, p)
		}
	}
	return out
}
