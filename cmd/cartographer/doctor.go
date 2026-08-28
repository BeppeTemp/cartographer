package main

// `cartographer doctor` (D143): read-only diagnosis of this machine's client
// configuration. The information already existed, scattered across commands
// that each answer a narrower question — `status` compares revisions, `agents`
// lists installed CLIs, `service status` covers the native unit — and none of
// them answers the one an operator actually has after an upgrade or a
// half-finished migration: is there anything left over here that should not be,
// or missing that should?
//
// Two rules the implementation must keep: it NEVER writes (no lockfile
// migration, no directory creation, no cache refresh), and every finding names
// a real path on this machine plus the command that fixes it. A diagnosis the
// operator cannot act on is noise, and a doctor that silently fixes things is a
// doctor nobody can predict — the repair paths already exist and are
// individually reviewable.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// doctorSchema is versioned like the status snapshot: this output ends up in
// someone's monitoring, so the shape is a contract.
const doctorSchema = "cartographer.doctor/v1"

// Severities, in report order.
const (
	// doctorError: something is broken now — a managed file missing, a hook
	// registered twice, an MCP entry pointing at a KB that no longer exists.
	doctorError = "error"
	// doctorWarning: something is stale or suboptimal — a v1 lockfile, no
	// trigger for a hook-less provider, a version difference.
	doctorWarning = "warning"
	// doctorInfo: context that is not actionable on its own and never changes
	// the exit code (a lockfile predating materialized hashes, so nothing can
	// be verified for those entries).
	doctorInfo = "info"
)

type doctorFinding struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

type doctorReport struct {
	Schema   string          `json:"schema_version"`
	Errors   int             `json:"error_count"`
	Warnings int             `json:"warning_count"`
	Infos    int             `json:"info_count"`
	Findings []doctorFinding `json:"findings"`
}

// cmdDoctor runs every check and reports. Exit 0 clean, 1 findings, 2 error —
// the same convention `status` uses, so CI treats them identically.
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "Emit the findings as JSON")
	provider := fs.String("provider", "", "Narrow the run to one provider")
	fs.Parse(args)

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if *provider != "" {
		if _, ok := configurator.Lookup(configurator.Provider(*provider)); !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown provider %q (want %s)\n", *provider, providerNamesJoined())
			return 2
		}
	}

	report := runDoctor(dir, *provider)
	code := doctorExitCode(report)
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	} else {
		printDoctorReport(report)
	}
	return code
}

// doctorExitCode maps a report to the exit code: 0 clean, 1 findings. Purely
// informational findings never fail the command — they are context, not a
// problem to act on.
func doctorExitCode(r doctorReport) int {
	if r.Errors > 0 || r.Warnings > 0 {
		return 1
	}
	return 0
}

// doctorDetectFn is indirected so tests can describe the machine instead of
// depending on which agent CLIs happen to be installed on it.
var doctorDetectFn = agents.Detect

// runDoctor executes every check against dir, optionally narrowed to one
// provider, and assembles the report. Each check degrades to a single finding
// rather than aborting the run: an unreadable provider config must not hide
// what the other seven checks would have said.
func runDoctor(dir, only string) doctorReport {
	report := doctorReport{Schema: doctorSchema}

	cfg, cfgErr := clientconfig.Load(dir)
	findings := checkClientConfig(dir, cfg, cfgErr, only)
	if cfgErr == nil {
		providers := doctorProviders(cfg, only)
		lockFile, lockErr := provisioning.ReadLockFile(lockFilePath(dir))
		findings = append(findings, checkLockfile(dir, providers, lockErr)...)
		if lockErr == nil {
			findings = append(findings, checkManagedFiles(dir, providers, lockFile)...)
			findings = append(findings, checkInstructionsBlock(dir, providers, lockFile)...)
			findings = append(findings, checkHookRegistrations(dir, providers, lockFile)...)
		}
		findings = append(findings, checkMCPEntries(dir, cfg, providers)...)
		findings = append(findings, checkServer(dir, cfg, providers)...)
		findings = append(findings, checkTriggerCoverage(dir, providers)...)
		findings = append(findings, checkSymlinkedDestinations(dir, providers)...)
	}

	sortDoctorFindings(findings)
	report.Findings = findings
	for _, f := range findings {
		switch f.Severity {
		case doctorError:
			report.Errors++
		case doctorWarning:
			report.Warnings++
		default:
			report.Infos++
		}
	}
	return report
}

// doctorProviders is the connected provider set the checks run over, narrowed
// by --provider. A provider named explicitly is inspected even if it is not
// connected: "why is this one not working" is a question doctor should answer.
func doctorProviders(cfg *clientconfig.Config, only string) []string {
	if only != "" {
		return []string{only}
	}
	return append([]string(nil), cfg.Agents...)
}

var doctorSeverityRank = map[string]int{doctorError: 0, doctorWarning: 1, doctorInfo: 2}

// sortDoctorFindings puts the actionable ones first and keeps the order stable
// within a severity, so two runs on an unchanged machine print the same thing.
func sortDoctorFindings(findings []doctorFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		return doctorSeverityRank[findings[i].Severity] < doctorSeverityRank[findings[j].Severity]
	})
}

func printDoctorReport(r doctorReport) {
	for _, f := range r.Findings {
		line := fmt.Sprintf("%-7s [%s] %s", f.Severity, f.Check, f.Message)
		if f.Path != "" {
			line += " — " + f.Path
		}
		fmt.Println(line)
		if f.Fix != "" {
			fmt.Printf("        fix: %s\n", f.Fix)
		}
	}
	if len(r.Findings) == 0 {
		fmt.Println("no findings")
		return
	}
	fmt.Printf("%d error(s), %d warning(s), %d informational\n", r.Errors, r.Warnings, r.Infos)
}

// ---------------------------------------------------------------- checks

// checkClientConfig: the file exists and parses, an agent is connected, and
// every connected provider is still installed on this machine.
func checkClientConfig(dir string, cfg *clientconfig.Config, loadErr error, only string) []doctorFinding {
	path := filepath.Join(dir, clientconfig.FileName)
	if loadErr != nil {
		return []doctorFinding{{
			Check: "client-config", Severity: doctorError, Path: path,
			Message: "no usable client configuration: " + loadErr.Error(),
			Fix:     "cartographer connect",
		}}
	}
	var out []doctorFinding
	if len(cfg.Agents) == 0 {
		out = append(out, doctorFinding{
			Check: "client-config", Severity: doctorWarning, Path: path,
			Message: "no agent is connected", Fix: "cartographer connect",
		})
	}
	installed := map[string]bool{}
	for _, a := range doctorDetectFn() {
		installed[string(a.Provider)] = a.Installed
	}
	for _, p := range doctorProviders(cfg, only) {
		if !installed[p] {
			out = append(out, doctorFinding{
				Check: "client-config", Severity: doctorWarning, Path: path,
				Message: fmt.Sprintf("provider %q is configured but no longer installed on this machine", p),
				Fix:     "cartographer disconnect " + p,
			})
		}
	}
	return out
}

// checkLockfile: present, readable, and in the v2 format. ReadLockFile
// migrates a v1 file in memory, but the file itself stays v1 until something
// rewrites it — doctor is not that something.
func checkLockfile(dir string, providers []string, readErr error) []doctorFinding {
	path := lockFilePath(dir)
	if readErr != nil {
		return []doctorFinding{{
			Check: "lockfile", Severity: doctorError, Path: path,
			Message: "lockfile cannot be read: " + readErr.Error(),
			Fix:     "cartographer reconnect",
		}}
	}
	format, err := provisioning.InspectLockFile(path)
	switch {
	case err != nil:
		return []doctorFinding{{
			Check: "lockfile", Severity: doctorError, Path: path,
			Message: "lockfile cannot be inspected: " + err.Error(),
			Fix:     "cartographer reconnect",
		}}
	case format == provisioning.LockFileAbsent:
		if len(providers) == 0 {
			return nil
		}
		return []doctorFinding{{
			Check: "lockfile", Severity: doctorWarning, Path: path,
			Message: "no lockfile: nothing has been materialized for the connected providers",
			Fix:     "cartographer sync",
		}}
	case format == provisioning.LockFileV1:
		return []doctorFinding{{
			Check: "lockfile", Severity: doctorWarning, Path: path,
			Message: "lockfile is still in the v1 single-provider format",
			Fix:     "cartographer sync",
		}}
	}
	return nil
}

// checkManagedFiles: D139's on-disk verification, per provider.
func checkManagedFiles(dir string, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	unknown := 0
	for _, p := range providers {
		lock := lockFile.ForProvider(p)
		baseDir := provisioning.LockBaseDir(lock, dir)
		for _, f := range provisioning.VerifyManaged(lock, configurator.Provider(p), baseDir) {
			full := filepath.Join(baseDir, f.Path)
			switch f.Reason {
			case provisioning.DriftUnknown:
				unknown++
			case provisioning.DriftError:
				out = append(out, doctorFinding{
					Check: "managed-files", Severity: doctorError, Path: full,
					Message: fmt.Sprintf("[%s] %s %q could not be verified: %s", p, f.Kind, f.Name, f.Detail),
				})
			default:
				out = append(out, doctorFinding{
					Check: "managed-files", Severity: doctorError, Path: full,
					Message: fmt.Sprintf("[%s] %s %q is %s on disk", p, f.Kind, f.Name, f.Reason),
					Fix:     "cartographer sync",
				})
			}
		}
	}
	if unknown > 0 {
		// Reported once, in aggregate: these entries predate materialized
		// hashes, so nothing about them can be verified — and treating that as
		// drift would rewrite every artifact on every client at once.
		//
		// The fix is `reconnect`, not `sync`: an entry with no materialized
		// hash is DriftUnknown, which is deliberately not healable, and
		// ComputeDiff sees no change either, so a sync re-materializes only
		// the artifacts whose content actually changed and leaves the rest
		// exactly as they are. Only a rebuild rewrites all of them, and with
		// them their hashes.
		out = append(out, doctorFinding{
			Check: "managed-files", Severity: doctorInfo, Path: lockFilePath(dir),
			Message: fmt.Sprintf("%d managed artifact(s) recorded before content hashes existed cannot be verified", unknown),
			Fix:     "cartographer reconnect",
		})
	}
	return out
}

// checkMCPEntries: the Cartographer entries in each provider's native config
// match the KBs recorded in .cartographer.yaml. An entry for a KB no longer
// mounted keeps pointing an agent at something that is gone.
func checkMCPEntries(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	entries, err := entriesForKBs(cfg.ServerName, cfg.ServerURL, cfg.KBs)
	if err != nil {
		return []doctorFinding{{
			Check: "mcp-entries", Severity: doctorError, Path: filepath.Join(dir, clientconfig.FileName),
			Message: "cannot derive the expected MCP entries: " + err.Error(),
			Fix:     "cartographer reconnect",
		}}
	}
	expected := map[string]bool{}
	for _, name := range entryNames(entries) {
		expected[name] = true
	}

	var out []doctorFinding
	for _, p := range providers {
		provider := configurator.Provider(p)
		d, ok := configurator.Lookup(provider)
		if !ok || !d.ManagesMCPConfig() {
			// A provider whose MCP configuration Cartographer does not own
			// (D141) has nothing to compare.
			continue
		}
		configPath := filepath.Join(dir, d.ConfigPath())
		declared, err := provisioning.MCPServerEntryNames(dir, provider)
		if err != nil {
			out = append(out, doctorFinding{
				Check: "mcp-entries", Severity: doctorError, Path: configPath,
				Message: fmt.Sprintf("[%s] MCP configuration cannot be read: %s", p, err),
				Fix:     "cartographer reconnect",
			})
			continue
		}
		present := map[string]bool{}
		for _, name := range declared {
			// Only the names this client owns: everything else in that file is
			// someone else's MCP server and none of doctor's business.
			if ownsMCPEntryName(cfg.ServerName, name) {
				present[name] = true
			}
		}
		for name := range present {
			if !expected[name] {
				out = append(out, doctorFinding{
					Check: "mcp-entries", Severity: doctorError, Path: configPath,
					Message: fmt.Sprintf("[%s] MCP entry %q is for a KB the server no longer mounts", p, name),
					Fix:     "cartographer reconnect",
				})
			}
		}
		for name := range expected {
			if !present[name] {
				out = append(out, doctorFinding{
					Check: "mcp-entries", Severity: doctorError, Path: configPath,
					Message: fmt.Sprintf("[%s] MCP entry %q is missing", p, name),
					Fix:     "cartographer reconnect",
				})
			}
		}
	}
	return out
}

// ownsMCPEntryName reports whether an MCP server entry belongs to this client:
// the configured server name itself, or one of its per-KB `<name>-<kb>` forms —
// the same namespace `disconnect` claims (managedEntryNames).
func ownsMCPEntryName(serverName, entry string) bool {
	return entry == serverName || strings.HasPrefix(entry, serverName+"-")
}

// checkInstructionsBlock: exactly one well-formed managed block per provider
// that has instructions materialized. Two begin markers mean the block was
// appended twice and the agent reads it twice; an unterminated one means
// everything after it is inside the block for the next rewrite.
func checkInstructionsBlock(dir string, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	for _, p := range providers {
		provider := configurator.Provider(p)
		rel := provisioning.InstructionsFile(provider)
		if rel == "" {
			continue
		}
		lock := lockFile.ForProvider(p)
		managed := false
		for _, mf := range lock.Managed {
			if mf.Kind == "instructions" {
				managed = true
				break
			}
		}
		path := filepath.Join(provisioning.LockBaseDir(lock, dir), rel)
		begins, ends, err := provisioning.InstructionsBlockMarkers(path)
		if err != nil {
			out = append(out, doctorFinding{
				Check: "instructions", Severity: doctorError, Path: path,
				Message: fmt.Sprintf("[%s] instructions file cannot be read: %s", p, err),
				Fix:     "cartographer reconnect",
			})
			continue
		}
		switch {
		case managed && begins == 0:
			out = append(out, doctorFinding{
				Check: "instructions", Severity: doctorError, Path: path,
				Message: fmt.Sprintf("[%s] the managed instructions block is gone", p),
				Fix:     "cartographer sync",
			})
		case begins > 1 || ends > 1:
			out = append(out, doctorFinding{
				Check: "instructions", Severity: doctorError, Path: path,
				Message: fmt.Sprintf("[%s] the managed instructions block appears %d time(s)", p, max(begins, ends)),
				Fix:     "cartographer reconnect",
			})
		case begins == 1 && ends == 0:
			out = append(out, doctorFinding{
				Check: "instructions", Severity: doctorError, Path: path,
				Message: fmt.Sprintf("[%s] the managed instructions block is not terminated", p),
				Fix:     "cartographer reconnect",
			})
		}
	}
	return out
}

// checkHookRegistrations: one native registration per managed hook. The D99
// double-fire — a marker-less copy left outside the managed block by Codex's
// own rewrite — is the case this check exists for; `sync` already repairs it.
func checkHookRegistrations(dir string, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	for _, p := range providers {
		provider := configurator.Provider(p)
		lock := lockFile.ForProvider(p)
		baseDir := provisioning.LockBaseDir(lock, dir)
		regFile := provisioning.HookRegistrationFile(provider)
		if regFile == "" {
			continue
		}
		path := filepath.Join(baseDir, regFile)
		for _, name := range managedHookNames(lock) {
			managed, stray, err := provisioning.HookRegistrations(baseDir, provider, name)
			if err != nil {
				out = append(out, doctorFinding{
					Check: "hooks", Severity: doctorError, Path: path,
					Message: fmt.Sprintf("[%s] hook %q registration cannot be read: %s", p, name, err),
					Fix:     "cartographer sync",
				})
				continue
			}
			if stray > 0 {
				out = append(out, doctorFinding{
					Check: "hooks", Severity: doctorError, Path: path,
					Message: fmt.Sprintf("[%s] hook %q has %d registration(s) outside the managed block: it fires more than once", p, name, stray),
					Fix:     "cartographer sync",
				})
			}
			if managed > 1 {
				out = append(out, doctorFinding{
					Check: "hooks", Severity: doctorError, Path: path,
					Message: fmt.Sprintf("[%s] hook %q is registered %d times", p, name, managed),
					Fix:     "cartographer sync",
				})
			}
		}
	}
	return out
}

// managedHookNames returns the distinct hook names the lockfile records.
func managedHookNames(lock provisioning.Lock) []string {
	var names []string
	seen := map[string]bool{}
	for _, mf := range lock.Managed {
		if mf.Kind != "hook" || seen[mf.Name] {
			continue
		}
		seen[mf.Name] = true
		names = append(names, mf.Name)
	}
	sort.Strings(names)
	return names
}

// checkServer: reachability, then the two version comparisons. An unreachable
// server is one finding, not a failure — doctor must stay useful offline.
func checkServer(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	configPath := filepath.Join(dir, clientconfig.FileName)
	facts, err := enumerateKBs(cfg.ServerURL, cfg.Auth, cfg.TokenEnv)
	if err != nil {
		return []doctorFinding{{
			Check: "server", Severity: doctorWarning, Path: configPath,
			Message: fmt.Sprintf("server %s is unreachable: %s", cfg.ServerURL, err),
			Fix:     "cartographer service status",
		}}
	}

	var out []doctorFinding
	if notice := serverChangeNotice(dir, providers, facts.Version); notice != "" {
		out = append(out, doctorFinding{
			Check: "server", Severity: doctorWarning, Path: lockFilePath(dir),
			Message: notice, Fix: "cartographer reconnect",
		})
	}
	if versionIsComparable(version) && versionIsComparable(facts.Version) && version != facts.Version {
		fix := "upgrade the client to match the server"
		if isLoopbackURL(cfg.ServerURL) {
			fix = "cartographer upgrade-repair"
		}
		out = append(out, doctorFinding{
			Check: "server", Severity: doctorWarning, Path: configPath,
			Message: fmt.Sprintf("version skew: client %s ≠ server %s", version, facts.Version),
			Fix:     fix,
		})
	}
	return out
}

// checkSymlinkedDestinations: a managed destination that is a symlink makes
// provisioning write wherever it points — into an unrelated git repository, in
// the case that produced D148. Apply refuses such an artifact, but only on a run
// where it is in the diff, so the condition is otherwise invisible. Answerable
// with no server, like the rest of doctor.
func checkSymlinkedDestinations(dir string, providers []string) []doctorFinding {
	var out []doctorFinding
	for _, p := range providers {
		for _, path := range provisioning.ManagedDestinationRoots(configurator.Provider(p), dir) {
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, readErr := os.Readlink(path)
			if readErr != nil {
				target = "(unreadable)"
			}
			out = append(out, doctorFinding{
				Check: "symlink", Severity: doctorWarning, Path: path,
				Message: fmt.Sprintf("[%s] %s is a symlink: provisioning writes would land in %s, so Cartographer refuses them", p, path, target),
				Fix:     "replace the symlink with a real directory, or point the client base dir at the real location",
			})
		}
	}
	return out
}

// checkTriggerCoverage: a provider with no session hook syncs only when a human
// remembers to, unless the scheduled trigger is installed (D140). Shares its
// predicate with printSyncTimerHint (connect.go) so the two cannot disagree.
func checkTriggerCoverage(dir string, providers []string) []doctorFinding {
	hookless, st := providersNeedingSyncTimer(providers)
	if len(hookless) == 0 {
		return nil
	}
	path := st.Path
	if path == "" {
		path = filepath.Join(dir, clientconfig.FileName)
	}
	return []doctorFinding{{
		Check: "trigger", Severity: doctorWarning, Path: path,
		Message: fmt.Sprintf("%s has no session-start hook and the scheduled trigger is not installed: it syncs only on demand", strings.Join(hookless, ", ")),
		Fix:     "cartographer service sync-timer install",
	}}
}
