package main

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// reportUsage is the post-apply step of `sync` that tells each server which
// skills and agents this machine's clients actually loaded (D326). It reads
// the local session transcripts (provisioning.ScanUsage, read-only) and posts
// only the per-artifact aggregate — name, kind, provider, last use, count — to
// the KB the artifact came from. No prompt, no message, no session id leaves
// the machine.
//
// It is best-effort and never blocks or fails a sync: a server too old to have
// the route, an unreachable one, a refused token all end in a debug line. It
// returns the number of entries the server accepted, for tests.
func reportUsage(cfg *clientconfig.Config, dir string) int {
	if !cfg.UsageScan {
		return 0
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		slog.Debug("usage report skipped: lockfile unreadable", "err", err)
		return 0
	}
	usage, err := provisioning.ScanUsage(lf, dir, provisioning.DefaultUsageWindow, true)
	if err != nil || len(usage) == 0 {
		return 0
	}
	// Per source KB; a bundled artifact is the binary's, not any KB's.
	byKB := map[string][]provisioning.ArtifactUsage{}
	for _, u := range usage {
		if name, ok := strings.CutPrefix(u.Source, "kb:"); ok && name != "" {
			byKB[name] = append(byKB[name], u)
		}
	}
	kbNames := make([]string, 0, len(byKB))
	for name := range byKB {
		kbNames = append(kbNames, name)
	}
	sort.Strings(kbNames)
	c := client.New(cfg.ServerURL, resolveToken(cfg)).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnvName(cfg))
	sent := 0
	for _, name := range kbNames {
		if err := c.ReportUsage(name, byKB[name], probeTimeout); err != nil {
			slog.Debug("usage report not delivered", "kb", name, "err", err)
			continue
		}
		sent += len(byKB[name])
	}
	return sent
}

// reportClientSettings is the post-apply step of `sync` that tells the server
// this machine's client settings (D818), for the Atlas Settings page to show
// the viewer their own client. Best-effort like reportUsage: it never blocks
// or fails a sync. The server keeps the report for the principal that sent it
// and shows it to that principal only.
func reportClientSettings(cfg *clientconfig.Config, dir string) bool {
	report, err := clientconfig.Report(dir, version)
	if err != nil {
		slog.Debug("client settings report skipped", "err", err)
		return false
	}
	report.Providers = reportProviders(cfg)
	report.Placeholders = reportPlaceholders(cfg, dir)
	c := client.New(cfg.ServerURL, resolveToken(cfg)).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnvName(cfg))
	if err := c.ReportClientSettings(report, probeTimeout); err != nil {
		slog.Debug("client settings report not delivered", "err", err)
		return false
	}
	return true
}

// reportOnly is `sync --report-to <url>`: the two reports above, sent to
// another server than the configured one, and nothing else. It is how
// `make dev DATA=…` shows this machine's real client on a live instance: the
// config is read, never saved, nothing is provisioned, and the configured
// server is not contacted.
func reportOnly(dir, url string) int {
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s: %v\n", dir, err)
		return 2
	}
	cfg.ServerURL = url
	if err := client.CheckServerURL(url, cfg.InsecureHTTPAllowed()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	usage := reportUsage(cfg, dir)
	settings := reportClientSettings(cfg, dir)
	fmt.Printf("reported to %s: %d usage entries, client settings %s\n", url, usage, map[bool]string{true: "sent", false: "not sent"}[settings])
	if !settings {
		return 1
	}
	return 0
}

// reportProviders describes each connected agent client: its scope, its bound
// folders and whether a binding narrows what a session sees (D737).
func reportProviders(cfg *clientconfig.Config) []clientconfig.ReportProvider {
	out := []clientconfig.ReportProvider{}
	for _, name := range cfg.Agents {
		p := clientconfig.ReportProvider{Name: name, Scope: cfg.WorkspaceScope(name), Entry: "http", Isolation: "none"}
		if cfg.UsesBridge(name, cfg.ServerRoutedPath) {
			p.Entry, p.Isolation = "bridge", "workspace"
		}
		for _, b := range cfg.Workspaces[name] {
			p.Workspaces = append(p.Workspaces, clientconfig.ReportWorkspace{Path: b.Path, Remote: b.Remote, KBs: b.KBs})
		}
		out = append(out, p)
	}
	return out
}

// reportPlaceholders is `paths list` as data: every key the last sync met,
// with where it resolved on this machine.
func reportPlaceholders(cfg *clientconfig.Config, dir string) []clientconfig.ReportPlaceholder {
	out := []clientconfig.ReportPlaceholder{}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return out
	}
	for _, r := range placeholderRows(lf) {
		out = append(out, clientconfig.ReportPlaceholder{
			Key: r.Key, KBs: r.KBs, Path: r.Path, Reason: r.Reason, Description: r.Description,
			Ignored: !r.Resolved() && containsString(cfg.IgnoredPaths, r.Key),
		})
	}
	return out
}
