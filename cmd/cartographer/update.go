package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// `cartographer update` (D254): tells an agent, through the session-start
// hook, that a newer release exists and how to install it. It never upgrades
// on its own unless the user opted into `update.policy: auto-patch`, and then
// only patch releases through a channel whose script is atomic.

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
	updateSettingsFn = loadUpdateSettings
	// updateStartDetachedFn starts `cartographer update apply` detached from
	// the hook that asked for it, so a session start never waits on an upgrade.
	updateStartDetachedFn = startDetachedApply
	// updateRunApplyFn runs the channel's own upgrade command.
	updateRunApplyFn = runApplyCommand
	updateNowFn      = time.Now
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
	case "apply":
		// Internal: started detached by the notice or sync under
		// update.policy: auto-patch. Not listed in the usage on purpose.
		return updateApply()
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

// loadUpdateSettings reads `update:` from .cartographer.yaml. A missing file
// is the default; an unreadable one is reported to the caller, which decides
// whether that may be said out loud (the hook never says it).
func loadUpdateSettings() (clientconfig.UpdateSettings, error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return clientconfig.UpdateSettings{}, err
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return clientconfig.UpdateSettings{}, nil
		}
		return clientconfig.UpdateSettings{}, err
	}
	return cfg.Update, nil
}

func updateOptions(settings clientconfig.UpdateSettings) (updatecheck.Options, string) {
	opts := updatecheck.Options{Disabled: !settings.CheckEnabled()}
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
	settings, _ := updateSettingsFn()
	opts, _ := updateOptions(settings)
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
	settings, serr := updateSettingsFn()
	if serr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (using the default update settings)\n", serr)
	}
	opts, _ := updateOptions(settings)
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
		fmt.Printf("latest    not checked (disabled by %s or update.check: false)\n", updatecheck.EnvDisable)
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
	settings, _ := updateSettingsFn()
	opts, cacheDir := updateOptions(settings)

	// A patch this machine applied to itself is announced once, by the
	// binary it installed.
	if cacheDir != "" {
		if m, ok := updatecheck.ReadMarker(cacheDir, true); ok && m.Version == version {
			updatecheck.ClearMarker(cacheDir)
			return fmt.Sprintf("Cartographer patched itself to %s; restart your agent sessions to load it. Tell the user once in this session.", m.Version)
		}
	}

	res, _ := updateCheckFn(context.Background(), opts)
	if !res.Available {
		return ""
	}
	ch := updateChannelFn()
	failure := ""
	if cacheDir != "" && updatecheck.ShouldAutoApply(settings.EffectivePolicy(), res, ch) {
		if m, ok := updatecheck.ReadMarker(cacheDir, false); ok && m.Version == res.Latest {
			failure = fmt.Sprintf(" The automatic patch to %s failed; its log is %s.", res.Latest, m.Log)
		} else if started, busy := startAutoPatch(cacheDir, res); started || busy {
			// Being handled in the background: the next session announces
			// the outcome.
			return ""
		}
	}
	return manualNotice(res, ch) + failure
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

// maybeAutoPatch is the scheduled sync's half of auto-patch: the same
// decision the notice makes, silently.
func maybeAutoPatch() {
	settings, err := updateSettingsFn()
	if err != nil || settings.EffectivePolicy() != updatecheck.PolicyAutoPatch {
		return
	}
	opts, cacheDir := updateOptions(settings)
	if cacheDir == "" {
		return
	}
	res, _ := updateCheckFn(context.Background(), opts)
	if !updatecheck.ShouldAutoApply(settings.EffectivePolicy(), res, updateChannelFn()) {
		return
	}
	if m, ok := updatecheck.ReadMarker(cacheDir, false); ok && m.Version == res.Latest {
		return // failed once for this version: the notice says so, no retry loop
	}
	startAutoPatch(cacheDir, res)
}

// startAutoPatch takes the apply lock and starts `update apply` detached.
// started is false and busy true when another session already started one.
func startAutoPatch(cacheDir string, res updatecheck.Result) (started, busy bool) {
	if m, ok := updatecheck.ReadMarker(cacheDir, true); ok && m.Version == res.Latest {
		// Applied, yet this binary is still the old one (another copy first
		// on PATH?): never re-apply, fall back to the manual notice.
		return false, false
	}
	release, err := updatecheck.AcquireApplyLock(cacheDir, updateNowFn())
	if errors.Is(err, updatecheck.ErrApplyInProgress) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	exe, err := os.Executable()
	if err == nil {
		err = updateStartDetachedFn([]string{exe, "update", "apply"})
	}
	if err != nil {
		release()
		return false, false
	}
	return true, false
}

// updateApply runs the channel's command and records the outcome for the
// next notice. The lock was taken by the process that started it.
func updateApply() int {
	cacheDir, err := updateCacheDirFn()
	if err != nil {
		return 1
	}
	defer updatecheck.ReleaseApplyLock(cacheDir)
	settings, err := updateSettingsFn()
	if err != nil {
		return 1
	}
	opts, _ := updateOptions(settings)
	res, _ := updateCheckFn(context.Background(), opts)
	ch := updateChannelFn()
	if !updatecheck.ShouldAutoApply(settings.EffectivePolicy(), res, ch) {
		return 0
	}
	logPath := filepath.Join(cacheDir, updatecheck.LogFileName)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 1
	}
	defer logf.Close()
	argv := ch.ApplyArgv()
	fmt.Fprintf(logf, "%s update apply %s → %s via %s: %q\n", updateNowFn().UTC().Format(time.RFC3339), res.Current, res.Latest, ch, argv)
	// The channel scripts are atomic: a failure leaves the binary as it was,
	// and the notice reverts to the manual form naming this log.
	if err := updateRunApplyFn(argv, logf); err != nil {
		fmt.Fprintf(logf, "failed: %v\n", err)
		_ = updatecheck.WriteMarker(cacheDir, false, updatecheck.Marker{Version: res.Latest, At: updateNowFn().UTC(), Log: logPath})
		return 1
	}
	fmt.Fprintln(logf, "done")
	_ = updatecheck.WriteMarker(cacheDir, true, updatecheck.Marker{Version: res.Latest, At: updateNowFn().UTC()})
	return 0
}

func runApplyCommand(argv []string, log io.Writer) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	// Homebrew and the installers read no input from here; NONINTERACTIVE
	// keeps Homebrew from prompting anyway.
	cmd.Env = append(os.Environ(), "NONINTERACTIVE=1")
	return cmd.Run()
}

// startDetachedApply starts argv in its own session with no inherited
// streams, and does not wait for it.
func startDetachedApply(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
