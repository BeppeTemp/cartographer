package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const statusHealthTimeout = 5 * time.Second

// Indirection keeps cmdStatus's version report independently testable without
// a network connection or a real launchd/systemd service.
var (
	statusHealthFn = func(cfg *clientconfig.Config) (*client.Health, error) {
		return client.New(cfg.ServerURL, resolveToken(cfg)).Health(statusHealthTimeout)
	}
	statusManifestFn = fetchMergedManifest
	statusServiceFn  = func() (service.Status, error) { return service.NewManager().Status("") }
)

// cmdStatus reports the sync status of every connected provider against the
// configured server: in-sync or drift, with added/updated/removed detail.
// Exit codes: 0 in-sync, 1 drift, 2 error (missing config, unreachable server, ...).
func cmdStatus(args []string) int {
	output, remaining, err := outputFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(remaining) != 0 {
		fmt.Fprintln(os.Stderr, "Error: usage: cartographer status [--output table|json]")
		return 2
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s (run `cartographer connect` first): %v\n", dir, err)
		return 2
	}
	if len(cfg.Agents) == 0 {
		return renderStatus(output, emptySnapshot(), 0)
	}
	s := snapshotForConfig(dir, cfg, true)
	code := 0
	if s.State == "drift" {
		code = 1
	}
	if s.State == "unavailable" || s.State == "error" {
		code = 2
	}
	return renderStatus(output, s, code)
}

func renderStatus(output string, s statusSnapshot, code int) int {
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(s)
		return code
	}
	if s.State == "not_configured" {
		fmt.Println("no agent connected (run `cartographer connect`)")
		return code
	}
	if s.Reachable {
		fmt.Printf("client %s — server %s (%s)\n", s.Client, s.Server, s.ServerURL)
	} else if s.Error != nil {
		fmt.Println(s.Error.Message)
	}
	if s.State == "version_skew" {
		fmt.Printf("version skew: client %s ≠ server %s\n", s.Client, s.Server)
		if s.Service != nil && s.Service.Installed {
			fmt.Println("local service may still run the old binary — run: cartographer service restart")
		}
	}
	for _, p := range s.Providers {
		if p.Connected {
			fmt.Printf("[%s] %s\n", p.Name, strings.ReplaceAll(p.State, "_", "-"))
		}
	}
	return code
}

// printVersionStatus reports the binary versions before the artifact status.
// A failed health request is intentionally non-fatal here: the following
// sync_pull still performs the existing artifact check and preserves its exit
// code/error behaviour. Version skew is advisory, never provisioning drift.
func printVersionStatus(cfg *clientconfig.Config) {
	health, err := statusHealthFn(cfg)
	if err != nil {
		fmt.Printf("client %s — server unreachable (%s)\n", version, cfg.ServerURL)
		return
	}

	fmt.Printf("client %s — server %s (%s)\n", version, health.Version, cfg.ServerURL)
	if version == "" || health.Version == "" || version == "dev" || health.Version == "dev" || version == health.Version {
		return
	}

	fmt.Printf("version skew: client %s ≠ server %s\n", version, health.Version)
	if !isLoopbackURL(cfg.ServerURL) {
		return
	}
	if st, err := statusServiceFn(); err == nil && st.Installed {
		fmt.Println("local service may still run the old binary — run: cartographer service restart")
	}
}
