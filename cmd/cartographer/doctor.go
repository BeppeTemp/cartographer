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
	"strconv"
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
	repairHashes := fs.Bool("repair-hashes", false, "Re-record the content hash of managed artifacts that have none, from the bytes already on disk (D157)")
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

	if *repairHashes {
		return repairManagedHashes(dir, *provider)
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

// repairManagedHashes backfills the materialized hash of every managed entry that
// has none, from the bytes on disk, and persists the lockfile (D157). Narrow on
// purpose: the alternative on offer was `reconnect`, which prunes and rewrites
// every managed artifact on every client — in the field ~150 file operations to
// backfill six hashes, with a partial failure leaving both clients without skills.
func repairManagedHashes(dir, only string) int {
	// Rewrites the lockfile, so it takes the client-state lock like every
	// other mutating path (D172). Read-only `doctor` does not.
	release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	defer release()

	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: read lockfile:", err)
		return 2
	}
	total := 0
	for providerName, lock := range lf.Providers {
		if only != "" && providerName != only {
			continue
		}
		repaired, skipped, repairErr := provisioning.RepairManagedHashes(lock, configurator.Provider(providerName), dir, false)
		if repairErr != nil {
			fmt.Fprintf(os.Stderr, "Error: repair %s: %v\n", providerName, repairErr)
			return 2
		}
		for _, mf := range repaired {
			fmt.Printf("[%s] re-recorded %s/%s from disk\n", providerName, mf.Kind, mf.Name)
		}
		for _, sk := range skipped {
			// A missing file is real drift for `sync` to fix, not something to
			// paper over with a hash of nothing.
			fmt.Printf("[%s] skipped %s/%s: %s\n", providerName, sk.Kind, sk.Name, sk.Reason)
		}
		total += len(repaired)
		lf.Providers[providerName] = lock
	}
	if total == 0 {
		fmt.Println("no unverifiable managed artifacts")
		return 0
	}
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		fmt.Fprintln(os.Stderr, "Error: write lockfile:", err)
		return 2
	}
	fmt.Printf("re-recorded %d managed artifact(s); they are adopted, not verified against the server\n", total)
	return 0
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
		findings = append(findings, checkCapabilities(dir, cfg)...)
		findings = append(findings, checkKBCollisions(dir, cfg, providers)...)
		if lockErr == nil {
			findings = append(findings, checkUnboundResidues(dir, cfg, providers, lockFile)...)
		}
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

// checkManagedFiles: D139's on-disk verification, per provider, plus the
// orphan report of D178.
func checkManagedFiles(dir string, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	out = append(out, checkOrphanedFiles(dir, providers, lockFile)...)
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
			// The content is already on disk and the hash is computable without
			// refetching anything, so the proportionate remedy is to re-record
			// it — `reconnect` rewrites every managed artifact on every client,
			// three orders of magnitude more work than the problem (D157).
			Fix: "cartographer doctor --repair-hashes (rebuild everything only if you actually want to: cartographer reconnect)",
		})
	}
	return out
}

// checkMCPEntries: the Cartographer entries in each provider's native config
// match the KBs recorded in .cartographer.yaml. An entry for a KB no longer
// mounted keeps pointing an agent at something that is gone.
func checkMCPEntries(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	entriesByProvider, err := entriesByProviderForKBs(cfg, providers, cfg.ServerName, cfg.ServerURL, cfg.KnownKBs)
	if err != nil {
		return []doctorFinding{{
			Check: "mcp-entries", Severity: doctorError, Path: filepath.Join(dir, clientconfig.FileName),
			Message: "cannot derive the expected MCP entries: " + err.Error(),
			Fix:     "cartographer reconnect",
		}}
	}
	var out []doctorFinding
	for _, p := range providers {
		// Expected entries are per provider since D170: a provider bound to a
		// subset must not be reported as missing the rest.
		expected := map[string]bool{}
		for _, name := range entryNames(entriesByProvider[p]) {
			expected[name] = true
		}
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

// checkOrphanedFiles reports files sitting inside a managed artifact
// directory that no lock entry accounts for (D178).
//
// D178 stops NEW orphans at the source, but a file stranded by an earlier
// version is absent from every lock and therefore invisible to pruning, to
// ComputeDiff and to the on-disk verification above — while an agent keeps
// reading it, since it is still inside a live skill or hook directory.
//
// It only REPORTS. doctor must not delete a file it cannot prove Cartographer
// wrote: a user may legitimately have added one. Removal is the operator's
// decision, or happens at the next sync once the artifact owns the file again.
func checkOrphanedFiles(dir string, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	for _, p := range providers {
		lock := lockFile.ForProvider(p)
		baseDir := provisioning.LockBaseDir(lock, dir)

		// The managed directories, and everything the lock accounts for in
		// them. A single-file kind (agent, mcp) has no directory of its own
		// and cannot strand anything.
		known := make(map[string]bool, len(lock.Managed))
		dirs := map[string]string{}
		for _, mf := range lock.Managed {
			known[filepath.Clean(mf.Path)] = true
			if mf.Kind != "skill" && mf.Kind != "hook" {
				continue
			}
			d := filepath.Dir(filepath.Clean(mf.Path))
			if d == "." || d == string(filepath.Separator) {
				continue
			}
			dirs[d] = mf.Kind + " " + strconv.Quote(mf.Name)
		}

		for rel, owner := range dirs {
			entries, err := os.ReadDir(filepath.Join(baseDir, rel))
			if err != nil {
				// A missing directory is real drift, already reported by the
				// on-disk verification: not this check's business.
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				path := filepath.Join(rel, e.Name())
				if known[path] {
					continue
				}
				out = append(out, doctorFinding{
					Check: "managed-files", Severity: doctorWarning, Path: filepath.Join(baseDir, path),
					Message: fmt.Sprintf("[%s] %s: %s is inside a managed directory but no lock entry accounts for it", p, owner, path),
					Fix:     "remove it if it is a leftover from an older sync; it is kept if you added it yourself",
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
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
		f := doctorFinding{
			Check: "server", Severity: doctorWarning, Path: configPath,
			Message: fmt.Sprintf("server %s is unreachable: %s", cfg.ServerURL, err),
			Fix:     "cartographer service status",
		}
		// The one configuration that cannot work rather than merely looking
		// unusual: the client points at this machine, nothing answers, and
		// there is no local service that ever could (D174). Sending the
		// operator to `service status` here would only report `installed:
		// false` — name the cause instead.
		if noLocalServiceFor(cfg.ServerURL) {
			f.Message = fmt.Sprintf("server %s is unreachable and no local cartographer service is installed to serve it", cfg.ServerURL)
			f.Fix = "cartographer service install, or point server_url at a running server"
		}
		return []doctorFinding{f}
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

// noLocalServiceFor reports whether rawURL names this machine and no native
// service is installed to answer it.
//
// A local service installed while the client points at a REMOTE server is
// deliberately not a finding: that coexistence is normal (a local test server
// plus a shared one), and its one real consequence — `kb` commands acting on
// the wrong data dir — belongs to its own check.
func noLocalServiceFor(rawURL string) bool {
	if !isLoopbackURL(rawURL) {
		return false
	}
	st, err := statusServiceFn()
	if err != nil {
		return false
	}
	return !st.Installed
}

// checkUnboundResidues: a managed file whose source KB is no longer bound to
// the provider holding it (D170). It survives a projection that predates an
// unbind, or a hand-edited lockfile, and nothing else reports it: the prune only
// removes what left the manifest, and after an unbind that provider's manifest
// is not even fetched for that KB.
//
// An empty Source is NOT a finding: every lockfile written before D170 has one,
// and unknown provenance is not wrong provenance. Reporting those would flag
// every existing machine on upgrade.
func checkUnboundResidues(dir string, cfg *clientconfig.Config, providers []string, lockFile provisioning.LockFile) []doctorFinding {
	var out []doctorFinding
	for _, p := range providers {
		bound, explicit := cfg.BoundKBs(p)
		if !explicit {
			// Bound to every known KB: a residue can only come from a KB the
			// server dropped, which checkMCPEntries already covers.
			continue
		}
		allowed := make(map[string]bool, len(bound))
		for _, kb := range bound {
			allowed["kb:"+kb] = true
		}
		lock := lockFile.ForProvider(p)
		baseDir := provisioning.LockBaseDir(lock, dir)
		reported := make(map[string]bool)
		for _, mf := range lock.Managed {
			if mf.Source == "" || mf.Source == "bundle" || allowed[mf.Source] {
				continue
			}
			key := mf.Kind + "\x00" + mf.Name
			if reported[key] {
				continue
			}
			reported[key] = true
			out = append(out, doctorFinding{
				Check: "unbound-residue", Severity: doctorError, Path: filepath.Join(baseDir, mf.Path),
				Message: fmt.Sprintf("[%s] %s/%s comes from %s, which is not bound to this provider", p, mf.Kind, mf.Name, mf.Source),
				Fix:     fmt.Sprintf("cartographer sync --client %s", p),
			})
		}
	}
	return out
}

// checkKBCollisions: two KBs bound to the same provider claiming one kind+name
// (D171). `sync` refuses outright when this happens, so finding it here is the
// difference between a diagnosis and a mystery — the sync error names the
// collision but a machine that has not synced since the binding changed shows
// no symptom at all.
//
// Silent when the server is unreachable: a missing signal is not evidence that
// nothing collides. Reported per provider, because two colliding KBs bound to
// different providers are not a conflict.
func checkKBCollisions(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	union, _, _ := boundKBUnion(cfg, providers)
	candidates, err := fetchCandidates(cfg, union)
	if err != nil {
		return nil
	}
	configPath := filepath.Join(dir, clientconfig.FileName)
	var out []doctorFinding
	for _, p := range providers {
		bound, _ := cfg.BoundKBs(p)
		for _, c := range collisionsForProvider(candidates.forKBs(bound), bound) {
			out = append(out, doctorFinding{
				Check: "kb-collisions", Severity: doctorError, Path: configPath,
				Message: fmt.Sprintf("%s: %s/%s is claimed by %s — sync refuses to run", p, c.Kind, c.Name, strings.Join(c.Sources, ", ")),
				Fix:     fmt.Sprintf("rename the artifact in all but one of those KBs, or cartographer client unbind %s <kb>", p),
			})
		}
	}
	return out
}

// checkCapabilities: a per-KB gate that is off changes what an agent can do, and
// nothing in the running system said so — a documented capability stayed unfound
// for a whole migration (D151). Silent when the server is unreachable or does
// not advertise capabilities: a missing signal is not evidence that everything is
// on.
func checkCapabilities(dir string, cfg *clientconfig.Config) []doctorFinding {
	facts, err := enumerateKBs(cfg.ServerURL, cfg.Auth, cfg.TokenEnv)
	if err != nil {
		return nil
	}
	configPath := filepath.Join(dir, clientconfig.FileName)
	var out []doctorFinding
	for _, kbInfo := range facts.KBs {
		names := make([]string, 0, len(kbInfo.Capabilities))
		for name := range kbInfo.Capabilities {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := kbInfo.Capabilities[name]
			switch {
			case c.State == "disabled":
				out = append(out, doctorFinding{
					Check: "capability", Severity: doctorInfo, Path: configPath,
					Message: fmt.Sprintf("KB %q has %s disabled", kbInfo.Name, name),
					Fix:     "set " + c.Setting + " for that KB in the server config",
				})
			case name == "mount" && c.State == "discovered":
				out = append(out, doctorFinding{
					Check: "capability", Severity: doctorInfo, Path: configPath,
					Message: fmt.Sprintf("KB %q was discovered under data:, so no kbs[] entry governs it and every per-KB setting is at its default", kbInfo.Name),
					Fix:     "add a kbs[] entry for it in the server config",
				})
			}
		}
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
