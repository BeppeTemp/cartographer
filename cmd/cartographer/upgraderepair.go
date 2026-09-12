package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// Exit codes for `cartographer upgrade-repair` (D121): 0 means complete
// repair or an intentional no-op; 1 means the native service replacement is
// already verified but provider sync is pending or failed; 2 means a running
// native service could not be safely replaced and verified (no sync is
// attempted in that case).
const (
	exitRepairOK             = 0
	exitRepairSyncPending    = 1
	exitRepairServiceFailure = 2
)

// repairHealthProbeTimeout bounds the single pre-check /health probe that
// decides whether the running service already serves the installed version
// (idempotent repair, no second drain). Manager.Replace uses its own bounded
// poll loop for the graceful-replacement case.
const repairHealthProbeTimeout = 2 * time.Second

// upgrade-repair's collaborators are indirected through package-level vars,
// mirroring statusServiceFn/statusHealthFn in status.go, so command tests
// exercise every state-matrix row without a real launchd/systemd service, a
// live /health endpoint, or a real sync_pull call.
var (
	repairServiceStatusFn    = func(configPath string) (service.Status, error) { return service.NewManager().Status(configPath) }
	repairEffectiveConfigFn  = func(explicit string) (string, error) { return service.NewManager().EffectiveConfigPath(explicit) }
	repairProbeHealthFn      = service.ProbeHealth
	repairReplaceFn          = func(opts service.ReplaceOptions) error { return service.NewManager().Replace(opts) }
	repairTargetDirFn        = clientconfig.TargetDir
	repairLoadClientConfigFn = clientconfig.Load
	repairRunSyncFn          = runSync
	repairSameBinaryFn       = sameExecutable
)

// cmdUpgradeRepair implements `cartographer upgrade-repair` (D121): a
// non-interactive, idempotent command run by installers (and available for
// manual retry) after a native local package upgrade. It gracefully replaces
// an already-running, out-of-date native service and proves the installed
// version is serving before reconciling already-configured local providers
// under the ordinary `cartographer sync` policy — never `disconnect`/
// `connect`, never a one-shot trust override.
func cmdUpgradeRepair(args []string) int {
	fs := flag.NewFlagSet("upgrade-repair", flag.ExitOnError)
	configFlag := fs.String("config", "", "Server config YAML used to verify the native service (default: discovered from the installed service definition, else the standard path)")
	fs.Parse(args)

	configPath, err := repairEffectiveConfigFn(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitRepairServiceFailure
	}

	st, err := repairServiceStatusFn(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitRepairServiceFailure
	}

	if !st.Installed {
		fmt.Println("native service not installed — nothing to repair")
		return exitRepairOK
	}
	if !st.Running {
		fmt.Println("native service is stopped — leaving it stopped; start it intentionally (`cartographer service start`), then run `cartographer sync`")
		return exitRepairOK
	}

	if err := ensureServiceCurrent(configPath, st.HTTPAddr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitRepairServiceFailure
	}

	return repairProviderSync(st.HTTPAddr)
}

// ensureServiceCurrent probes /health once and skips the graceful
// replacement entirely when it already reports the installed version
// (idempotent repair, avoids a second drain). Otherwise it calls
// Manager.Replace, which signals the process and blocks until proof.
func ensureServiceCurrent(configPath, httpAddr string) error {
	hs, healthErr := repairProbeHealthFn(httpAddr, repairHealthProbeTimeout)
	if healthErr == nil && hs.Status == "ok" && versionAlreadyCurrent(hs.Version) {
		return nil
	}
	if err := repairReplaceFn(service.ReplaceOptions{ConfigPath: configPath, ExpectedVersion: version}); err != nil {
		return fmt.Errorf("native service could not be verified: %w", err)
	}
	return nil
}

// repairStaleServiceBeforeSync is the lazy half of D121 (D199). The Homebrew
// Cask can no longer run upgrade-repair — `postflight_steps` execute in
// Homebrew's sandbox, with a temporary HOME, the real one unreadable and the
// network denied — so the next ordinary `cartographer sync` (session-start
// hook, scheduled timer or by hand) replaces a native service still running
// the previous binary. It acts only on unambiguous evidence: an installed,
// running service whose program is this very binary, reached over the
// loopback endpoint this client syncs against, answering /health with a
// different version. Anything else — an unreachable or unhealthy service
// included — is left to sync's own diagnostics, and a failed replacement is a
// warning: the sync proceeds either way.
func repairStaleServiceBeforeSync(serverURL string) {
	if version == "" || version == "dev" {
		return
	}
	configPath, err := repairEffectiveConfigFn("")
	if err != nil {
		return
	}
	st, err := repairServiceStatusFn(configPath)
	if err != nil || !st.Installed || !st.Running {
		return
	}
	if ok, _ := upgradeRepairSyncEligible(serverURL, st.HTTPAddr); !ok {
		return
	}
	// A service running another binary (an install.sh copy next to a Cask,
	// a dev build) would never report this version: replacing it would
	// restart it on every sync without ever converging.
	if !repairSameBinaryFn(st.BinPath) {
		return
	}
	hs, err := repairProbeHealthFn(st.HTTPAddr, repairHealthProbeTimeout)
	if err != nil || hs.Status != "ok" || hs.Version == "" || hs.Version == version {
		return
	}
	fmt.Printf("native service runs %s, this binary is %s — replacing it gracefully\n", hs.Version, version)
	if err := repairReplaceFn(service.ReplaceOptions{ConfigPath: configPath, ExpectedVersion: version}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: native service could not be verified after replacement: %v — retry with: cartographer upgrade-repair\n", err)
		return
	}
	fmt.Printf("native service now serves %s\n", version)
}

// sameExecutable reports whether binPath (the native service's program,
// Homebrew's stable symlink) resolves to the same file as this process.
func sameExecutable(binPath string) bool {
	if binPath == "" {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return false
	}
	binInfo, err := os.Stat(binPath)
	if err != nil {
		return false
	}
	return os.SameFile(selfInfo, binInfo)
}

// versionAlreadyCurrent mirrors service.Replace's own equality rule: an
// empty or "dev" build version is not uniquely identifiable, so only a
// healthy response is required.
func versionAlreadyCurrent(observed string) bool {
	return version == "" || version == "dev" || observed == version
}

// repairProviderSync loads the client config (if any) and runs the ordinary
// sync policy when the client is configured against this same native
// service over loopback HTTP. Every other case — missing config, zero
// providers, a non-loopback or different local endpoint — is a successful
// no-op: the service has already been verified, and this command never
// contacts a possibly remote server on its own initiative.
func repairProviderSync(serviceHTTPAddr string) int {
	dir, err := repairTargetDirFn()
	if err != nil {
		// The service is already proven at this point, so a client-side
		// failure is a pending sync (exit 1), never a service failure.
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Println("native service verified; provider sync pending — retry with: cartographer sync")
		return exitRepairSyncPending
	}

	cfg, err := repairLoadClientConfigFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("no client configuration found — nothing to synchronize")
			return exitRepairOK
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Println("native service verified; retry with: cartographer sync")
		return exitRepairSyncPending
	}

	if len(cfg.Agents) == 0 {
		fmt.Println("no agent connected — nothing to synchronize")
		return exitRepairOK
	}

	if ok, reason := upgradeRepairSyncEligible(cfg.ServerURL, serviceHTTPAddr); !ok {
		fmt.Printf("skipping provider sync: %s\n", reason)
		return exitRepairOK
	}

	if _, err := repairRunSyncFn(dir, cfg, syncOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Println("native service verified; provider sync pending — retry with: cartographer sync")
		return exitRepairSyncPending
	}

	fmt.Println("provider sync complete — restart already-open provider sessions to pick up the repaired configuration")
	return exitRepairOK
}

// upgradeRepairSyncEligible decides whether upgrade-repair may run provider
// sync against the local native service: the client's server_url must use
// HTTP on a loopback host, carry no embedded credentials or fragment, and
// its effective port must match the native service's bind port. Anything
// else — HTTPS, a non-loopback host, a malformed URL, or a port mismatch —
// skips sync instead of contacting a possibly remote server.
func upgradeRepairSyncEligible(clientURL, serviceHTTPAddr string) (bool, string) {
	u, err := url.Parse(clientURL)
	if err != nil {
		return false, fmt.Sprintf("client server_url is malformed: %v", err)
	}
	if u.Scheme != "http" {
		return false, fmt.Sprintf("client server_url uses %q, not a loopback http endpoint", u.Scheme)
	}
	if u.User != nil {
		return false, "client server_url has embedded credentials"
	}
	if u.Fragment != "" {
		return false, "client server_url has a fragment"
	}
	if !isLoopbackHost(u.Hostname()) {
		return false, fmt.Sprintf("client server_url host %q is not loopback", u.Hostname())
	}

	clientPort := u.Port()
	if clientPort == "" {
		clientPort = "80" // default HTTP port for an omitted URL port
	}
	servicePort, err := serviceBindPort(serviceHTTPAddr)
	if err != nil {
		return false, fmt.Sprintf("native service http address is not usable: %v", err)
	}
	if clientPort != servicePort {
		return false, fmt.Sprintf("client port %s does not match the native service port %s", clientPort, servicePort)
	}
	return true, ""
}

// isLoopbackHost treats localhost/127.0.0.1/::1 as equivalent loopback hosts.
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// serviceBindPort extracts the port from a native service http address,
// which may be a bare port (":39273", meaning all interfaces) or host:port.
func serviceBindPort(addr string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	return port, nil
}
