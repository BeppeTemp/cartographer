package main

// `cartographer doctor schedule|unschedule|status|run` (D369): an opt-in daily
// headless agent session that runs the kb-doctor skill, so a KB nobody opens an
// agent on still converges. Nothing here runs by itself: connect, setup and sync
// never install it, and the server (which has no model, D14) only learns that
// it exists through the declaration this file posts.
//
// The scheduler job is `cartographer doctor run ...`, not the client: the
// headless flags per client, the timeout and the re-declaration after each run
// are written once, here, instead of in three unit formats.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const (
	defaultDoctorAt = "06:00"
	// doctorRunTimeout bounds one headless session: the budget of the skill is
	// 40 review items, and an unattended run must not spend quota unwatched
	// for longer than that takes.
	doctorRunTimeout = 2 * time.Hour
)

// headlessClient is how one client is started non-interactively with a prompt.
// Only clients whose non-interactive invocation docs/harnesses.md documents are
// listed; Hermes has none documented and is left out until it does.
type headlessClient struct {
	bins []string // executable names, in lookup order
	args func(prompt string) []string
}

var headlessClients = map[string]headlessClient{
	string(configurator.ProviderClaudeCode):  {[]string{"claude"}, func(p string) []string { return []string{"-p", p} }},
	string(configurator.ProviderCodex):       {[]string{"codex"}, func(p string) []string { return []string{"exec", p} }},
	string(configurator.ProviderOpenCode):    {[]string{"opencode"}, func(p string) []string { return []string{"run", p} }},
	string(configurator.ProviderKiro):        {[]string{"kiro-cli", "kiro"}, func(p string) []string { return []string{"chat", "--no-interactive", p} }},
	string(configurator.ProviderAntigravity): {[]string{"agy"}, func(p string) []string { return []string{"-p", p} }},
	string(configurator.ProviderCrush):       {[]string{"crush"}, func(p string) []string { return []string{"run", "-q", p} }},
}

func headlessClientNames() string {
	names := make([]string, 0, len(headlessClients))
	for n := range headlessClients {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func doctorPrompt(kbName string) string {
	return fmt.Sprintf("Run the kb-doctor skill on kb %q unattended.", kbName)
}

// Seams for tests: no real scheduler, no real client, no network.
var (
	doctorLookPathFn     = exec.LookPath
	doctorNowFn          = time.Now
	doctorTimerInstallFn = func(args []string, h, m int, dir string) error {
		return service.NewManager().InstallDoctorTimer(args, h, m, dir)
	}
	doctorTimerRemoveFn   = func() error { return service.NewManager().UninstallDoctorTimer() }
	doctorTimerStatusFn   = func() (service.DoctorTimerStatus, error) { return service.NewManager().DoctorTimerStatus() }
	doctorDeclareFn       = declareDoctorSchedule
	doctorWithdrawFn      = withdrawDoctorSchedule
	doctorRunClientFn     = runHeadlessClient
	doctorLogPathFn       = doctorLogPath
	doctorDeclareTimeout  = probeTimeout
	doctorScheduleRunHome = os.UserHomeDir
)

// doctorLogPath is the --log-file of the job on Windows only: a Scheduled Task
// has nowhere to declare a log (D217), while launchd and systemd capture the
// output themselves.
func doctorLogPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	p, _ := service.DoctorWindowsLogPath()
	return p
}

var atRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func parseAt(s string) (h, m int, err error) {
	g := atRe.FindStringSubmatch(s)
	if g == nil {
		return 0, 0, fmt.Errorf("--at %q: want HH:MM, 24-hour local time", s)
	}
	fmt.Sscanf(g[1], "%d", &h)
	fmt.Sscanf(g[2], "%d", &m)
	return h, m, nil
}

// nextOccurrence is the first Hour:Minute strictly after now, in now's zone.
func nextOccurrence(now time.Time, h, m int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(now.Year(), now.Month(), now.Day()+1, h, m, 0, 0, now.Location())
	}
	return t
}

type repeatedFlag []string

func (r *repeatedFlag) String() string     { return strings.Join(*r, " ") }
func (r *repeatedFlag) Set(v string) error { *r = append(*r, v); return nil }

// cmdDoctorSchedule dispatches the scheduling subcommands; ok is false when
// args is not one of them (the caller then runs the read-only diagnosis).
func cmdDoctorSchedule(args []string) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "schedule":
		return doctorScheduleInstall(args[1:]), true
	case "unschedule":
		return doctorScheduleRemove(args[1:]), true
	case "status":
		return doctorScheduleStatus(args[1:]), true
	case "run":
		return doctorScheduleRun(args[1:]), true
	}
	return 0, false
}

func loadClientConfig() (*clientconfig.Config, error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return nil, err
	}
	return clientconfig.Load(dir)
}

func doctorScheduleInstall(args []string) int {
	fs := flag.NewFlagSet("doctor schedule", flag.ExitOnError)
	clientName := fs.String("client", "", "Agent client to run headless ("+headlessClientNames()+"); default: the only connected one that can")
	kbName := fs.String("kb", "", "KB to run the doctor on; default: the only one this client knows")
	at := fs.String("at", defaultDoctorAt, "Local time of day, HH:MM")
	var extra repeatedFlag
	fs.Var(&extra, "client-flag", "Extra argument passed to the client before the prompt (repeatable), e.g. a permission flag the client needs unattended")
	fs.Parse(args)

	hour, minute, err := parseAt(*at)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	cfg, err := loadClientConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	name := *clientName
	if name == "" {
		var capable []string
		for _, a := range cfg.Agents {
			if _, ok := headlessClients[a]; ok {
				capable = append(capable, a)
			}
		}
		if len(capable) != 1 {
			fmt.Fprintf(os.Stderr, "Error: --client required (%d connected clients can run headless; choose among %s)\n", len(capable), headlessClientNames())
			return exitStatusError
		}
		name = capable[0]
	}
	hc, ok := headlessClients[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: client %q has no documented headless mode (want %s)\n", name, headlessClientNames())
		return exitStatusError
	}
	kbn := *kbName
	if kbn == "" {
		if len(cfg.KnownKBs) != 1 {
			fmt.Fprintf(os.Stderr, "Error: --kb required (this client knows %d KBs)\n", len(cfg.KnownKBs))
			return exitStatusError
		}
		kbn = cfg.KnownKBs[0]
	}
	if !kbNameRe.MatchString(kbn) {
		fmt.Fprintf(os.Stderr, "Error: invalid KB name %q\n", kbn)
		return exitStatusError
	}
	var binPath string
	for _, b := range hc.bins {
		if p, err := doctorLookPathFn(b); err == nil {
			binPath = p
			break
		}
	}
	if binPath == "" {
		fmt.Fprintf(os.Stderr, "Error: %s not found on PATH (looked for %s)\n", name, strings.Join(hc.bins, ", "))
		return exitStatusError
	}
	if abs, err := filepath.Abs(binPath); err == nil {
		binPath = abs
	}

	jobArgs := []string{"doctor", "run", "--kb", kbn, "--client", name, "--client-bin", binPath, "--at", *at}
	for _, e := range extra {
		jobArgs = append(jobArgs, "--client-flag", e)
	}
	if lp := doctorLogPathFn(); lp != "" {
		jobArgs = append(jobArgs, "--log-file", lp)
	}
	if err := doctorTimerInstallFn(jobArgs, hour, minute, filepath.Dir(binPath)); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	next := nextOccurrence(doctorNowFn(), hour, minute)
	fmt.Printf("scheduled: %s runs the kb-doctor skill on %q daily at %02d:%02d (next %s)\n", name, kbn, hour, minute, next.Format("2006-01-02 15:04 MST"))
	fmt.Println("each run spends that client's model quota unattended, up to 2h; undo it with `cartographer doctor unschedule`")
	if err := doctorDeclareFn(cfg, kbn, name, next); err != nil {
		fmt.Printf("warning: the server was not told (%v); the first run will declare it\n", err)
	}
	return 0
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// doctorJobFlags reads --kb/--client/--at back out of an installed definition:
// strip markup and quotes, split on whitespace, take the word after each flag.
// The values are validated to carry no quote or space when scheduled, so one
// scan serves a plist, a systemd unit and a task XML alike.
func doctorJobFlags(definition string) (kbName, clientName, at string) {
	words := strings.Fields(strings.ReplaceAll(tagRe.ReplaceAllString(definition, " "), `"`, " "))
	grab := func(flagName string) string {
		for i, w := range words {
			if w == "--"+flagName && i+1 < len(words) {
				return words[i+1]
			}
		}
		return ""
	}
	return grab("kb"), grab("client"), grab("at")
}

func doctorScheduleRemove(args []string) int {
	fs := flag.NewFlagSet("doctor unschedule", flag.ExitOnError)
	fs.Parse(args)
	st, _ := doctorTimerStatusFn()
	kbn, _, _ := doctorJobFlags(st.Definition)
	if err := doctorTimerRemoveFn(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("scheduled doctor removed")
	if kbn != "" {
		if cfg, err := loadClientConfig(); err == nil {
			if err := doctorWithdrawFn(cfg, kbn); err != nil {
				fmt.Printf("warning: the server was not told (%v); its declaration expires a day after its next run\n", err)
			}
		}
	}
	return 0
}

func doctorScheduleStatus(args []string) int {
	fs := flag.NewFlagSet("doctor status", flag.ExitOnError)
	fs.Parse(args)
	st, err := doctorTimerStatusFn()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	if !st.Installed {
		fmt.Println("no scheduled doctor (set one up with `cartographer doctor schedule`)")
		return exitStatusNotInstalled
	}
	kbn, clientName, at := doctorJobFlags(st.Definition)
	state := "active"
	code := exitStatusRunning
	if !st.Active {
		state, code = "installed but not active", exitStatusStopped
	}
	fmt.Printf("scheduled doctor: %s\n  client %s, kb %q, daily at %s\n  %s\n", state, clientName, kbn, at, st.Path)
	if h, m, err := parseAt(at); err == nil {
		fmt.Printf("  next run %s\n", nextOccurrence(doctorNowFn(), h, m).Format("2006-01-02 15:04 MST"))
	}
	return code
}

// doctorScheduleRun is the scheduler job: run the client headless, and only when
// it exits 0 declare the following run, so a client that keeps failing lets the
// server's declaration go stale instead of promising sessions that do not happen.
func doctorScheduleRun(args []string) int {
	fs := flag.NewFlagSet("doctor run", flag.ExitOnError)
	kbn := fs.String("kb", "", "KB")
	clientName := fs.String("client", "", "client")
	clientBin := fs.String("client-bin", "", "absolute path of the client executable")
	at := fs.String("at", defaultDoctorAt, "scheduled time of day, HH:MM")
	logFile := fs.String("log-file", "", "append this run's output to this file")
	var extra repeatedFlag
	fs.Var(&extra, "client-flag", "extra client argument (repeatable)")
	fs.Parse(args)
	if *logFile != "" {
		f, err := redirectClientOutput(*logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		defer f.Close()
	}
	hc, ok := headlessClients[*clientName]
	hour, minute, atErr := parseAt(*at)
	if !ok || *kbn == "" || *clientBin == "" || atErr != nil {
		fmt.Fprintln(os.Stderr, "Error: doctor run needs --kb, --client (headless-capable), --client-bin and a valid --at")
		return exitStatusError
	}
	argv := append(append([]string{}, extra...), hc.args(doctorPrompt(*kbn))...)
	fmt.Printf("%s doctor run: %s %s\n", doctorNowFn().Format(time.RFC3339), *clientName, *kbn)
	if err := doctorRunClientFn(*clientBin, argv); err != nil {
		fmt.Fprintln(os.Stderr, "Error: client session failed:", err)
		return exitStatusError
	}
	if cfg, err := loadClientConfig(); err == nil {
		if err := doctorDeclareFn(cfg, *kbn, *clientName, nextOccurrence(doctorNowFn(), hour, minute)); err != nil {
			fmt.Println("warning: the server was not told:", err)
		}
	}
	return 0
}

func runHeadlessClient(bin string, argv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, argv...)
	if home, err := doctorScheduleRunHome(); err == nil {
		cmd.Dir = home
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// stdin stays nil (the null device): `codex exec` waits on an open stdin.
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s", doctorRunTimeout)
	}
	return err
}

func newDoctorAPIClient(cfg *clientconfig.Config) *client.MCPClient {
	return client.New(cfg.ServerURL, resolveToken(cfg)).WithTokenEnv(tokenEnvName(cfg))
}

func declareDoctorSchedule(cfg *clientconfig.Config, kbName, clientName string, next time.Time) error {
	return newDoctorAPIClient(cfg).DeclareDoctorSchedule(kbName, clientName, next, doctorDeclareTimeout)
}

func withdrawDoctorSchedule(cfg *clientconfig.Config, kbName string) error {
	return newDoctorAPIClient(cfg).WithdrawDoctorSchedule(kbName, doctorDeclareTimeout)
}
