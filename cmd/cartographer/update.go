package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// `cartographer update` (D254): tells an agent, through the session-start
// hook, that a newer release exists and how to install it. It never upgrades
// on its own (D906): there is no auto-patch, only the notice and the command
// that upgrades.

// Indirections so tests never reach GitHub, the real cache directory or a
// real package manager.
var (
	updateCacheDirFn = updatecheck.CacheDir
	updateCheckFn    = func(ctx context.Context, opts updatecheck.Options) (updatecheck.Result, error) {
		return updatecheck.Check(ctx, version, opts)
	}
	updateChannelFn = func() updatecheck.Channel {
		exe, err := os.Executable()
		if err != nil {
			return updatecheck.ChannelUnknown
		}
		return updatecheck.DetectChannel(exe)
	}
	updateSettingsFn = updateCheckEnabled
)

// updateView is the `update check --output json` object. Additive only.
type updateView struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Kind      string `json:"kind,omitempty"`
	Channel   string `json:"channel"`
	Command   string `json:"command,omitempty"`
}

func cmdUpdate(args []string) int {
	if len(args) == 0 {
		printUpdateUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "check":
		return updateCheck(args[1:])
	case "notice":
		if len(args) != 1 {
			printUpdateUsage(os.Stderr)
			return 2
		}
		return updateNotice()
	case "help", "-h", "--help":
		printUpdateUsage(os.Stdout)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q\n", args[0])
	printUpdateUsage(os.Stderr)
	return 2
}

func printUpdateUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cartographer update check [--output table|json]")
	fmt.Fprintln(w, "       cartographer update notice")
}

// updateCheckEnabled reads `update_check` from .cartographer.yaml: true when
// the file is missing or does not say. An unreadable file is reported to the
// caller, which decides whether that may be said out loud (the hook never says
// it).
func updateCheckEnabled() (bool, error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return true, err
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return true, err
	}
	return cfg.UpdateCheckEnabled(), nil
}

func updateOptions(enabled bool) (updatecheck.Options, string) {
	opts := updatecheck.Options{Disabled: !enabled}
	dir, err := updateCacheDirFn()
	if err != nil {
		return opts, ""
	}
	opts.CacheFile = filepath.Join(dir, updatecheck.CacheFileName)
	return opts, dir
}

// cachedUpdate answers from the cache only: status, doctor and the dashboard
// must not add network latency. The hook and `update check` refresh it.
func cachedUpdate() (updatecheck.Result, updatecheck.Channel) {
	enabled, _ := updateSettingsFn()
	opts, _ := updateOptions(enabled)
	opts.CacheOnly = true
	res, _ := updateCheckFn(context.Background(), opts)
	if !res.Available {
		return res, ""
	}
	return res, updateChannelFn()
}

func updateCheck(args []string) int {
	output, rest, err := outputFlag(args)
	if err != nil || len(rest) != 0 {
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		printUpdateUsage(os.Stderr)
		return 2
	}
	enabled, serr := updateSettingsFn()
	if serr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (using the default update settings)\n", serr)
	}
	opts, _ := updateOptions(enabled)
	opts.Force = true
	res, checkErr := updateCheckFn(context.Background(), opts)
	ch := updateChannelFn()
	view := updateView{Current: res.Current, Latest: res.Latest, Available: res.Available, Kind: res.Kind, Channel: string(ch), Command: ch.UpgradeCommand(res.Latest)}
	if view.Current == "" {
		view.Current = version
	}
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(view)
		return 0
	}
	fmt.Printf("current   %s\n", view.Current)
	switch {
	case view.Current == "dev" || view.Current == "":
		fmt.Println("latest    not checked (development build)")
	case errors.Is(checkErr, updatecheck.ErrDisabled):
		fmt.Printf("latest    not checked (disabled by %s or update_check: false)\n", updatecheck.EnvDisable)
	case view.Latest == "":
		fmt.Printf("latest    unknown (%v)\n", checkErr)
	case view.Available:
		fmt.Printf("latest    %s (%s update available)\n", view.Latest, view.Kind)
	default:
		fmt.Printf("latest    %s (up to date)\n", view.Latest)
	}
	fmt.Printf("channel   %s\n", view.Channel)
	if view.Command != "" {
		fmt.Printf("command   %s\n", view.Command)
	} else {
		fmt.Printf("command   none for this channel — see %s\n", updatecheck.ReleasesURL)
	}
	return 0
}

// updateNotice is what the session-start hook runs. It prints nothing unless
// an update exists, then exactly one paragraph addressed to the agent, and it
// always exits 0: a hook must never break a session start.
func updateNotice() (code int) {
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	if text := noticeText(); text != "" {
		fmt.Println(text)
	}
	return 0
}

func noticeText() string {
	enabled, _ := updateSettingsFn()
	opts, _ := updateOptions(enabled)
	// D320: a cache that only knows the running version is re-checked, so a
	// release published inside the TTL window reaches the next session.
	opts.ForceRefreshOnVersion = version

	res, _ := updateCheckFn(context.Background(), opts)
	if !res.Available {
		return ""
	}
	return manualNotice(res, updateChannelFn())
}

// manualNotice carries the behaviour itself, so it works whatever the KB's
// instructions say (D254): tell once, offer, act only on consent.
func manualNotice(res updatecheck.Result, ch updatecheck.Channel) string {
	how := ""
	switch cmd := ch.UpgradeCommand(res.Latest); {
	case cmd != "":
		how = "Upgrade with: " + cmd + "."
	case ch == updatecheck.ChannelContainer:
		how = "This runs in a container: upgrade by updating the Cartographer image tag."
	default:
		how = "How to upgrade depends on how it was installed: see " + updatecheck.ReleasesURL + "."
	}
	return fmt.Sprintf("Cartographer update available: %s (installed %s, %s). %s Release notes: %s/tag/%s. "+
		"Tell the user once in this session and offer to run it; run it only if they agree, then follow the "+
		"cartographer-ops skill (§Upgrade) — upgrade-repair, reconnect if the release notes require it, and ask them "+
		"to restart their agent sessions.",
		res.Latest, res.Current, res.Kind, how, updatecheck.ReleasesURL, res.Latest)
}
