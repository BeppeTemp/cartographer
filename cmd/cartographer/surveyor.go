package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

// Exit codes of `cartographer surveyor`: 0 graceful shutdown, 1 runtime failure,
// 2 start error (nothing was started).
const (
	surveyorExitRuntime = 1
	surveyorExitStart   = 2
)

// startError is a failure before the surveyor started, with its exit code.
type startError struct {
	code int
	msg  string
}

func (e *startError) Error() string { return e.msg }

func startFail(code int, format string, args ...any) error {
	return &startError{code: code, msg: fmt.Sprintf(format, args...)}
}

// retryPolicy bounds how long start-up waits for a server that is bootstrapping
// or restarting; tests shorten it.
type retryPolicy struct {
	total, first, max time.Duration
	sleep             func(ctx context.Context, d time.Duration) bool // false when ctx ended
}

var defaultRetry = retryPolicy{total: 5 * time.Minute, first: 2 * time.Second, max: 30 * time.Second, sleep: sleepCtx}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

const startupCallTimeout = 10 * time.Second

// surveyorEnv is what a successful start-up hands to the runner.
type surveyorEnv struct {
	cfg    *surveyor.Config
	client *client.MCPClient
	models map[string]surveyor.Model // by profile name
	// settings is the report posted to the server (typed entries, no secret).
	settings *surveyorconfig.Report
}

// closeTimeout bounds each closing log_append on shutdown; the entries are
// written in parallel, so the total stays well inside Kubernetes' default 30 s
// terminationGracePeriodSeconds.
const closeTimeout = 25 * time.Second

// toolCallTimeout bounds one tool call of a task. The client's default 30 s is
// for interactive use: kb_repair over a whole KB takes over a minute, and a call
// the client abandons keeps running on the server, holding the KB's lock.
const toolCallTimeout = 5 * time.Minute

// surveyorRunFn is the runner behind the start-up checks (stubbed by tests).
var surveyorRunFn = func(ctx context.Context, env *surveyorEnv, once bool) int {
	tiers := map[string]string{}
	for _, p := range env.cfg.Models {
		tiers[p.Name] = p.Tier
	}
	beater := &surveyor.Beater{API: env.client, Perimeter: env.cfg.Name, Version: version, KBs: env.cfg.KBs,
		Tiers: tiers, Tasks: surveyor.DeclaredTasks(env.cfg), Settings: env.settings, Logf: log.Printf}
	r, err := surveyor.NewRunner(env.cfg, surveyor.DefaultKinds(), surveyor.RunnerOptions{
		Callers:      func(kb string) surveyor.ToolCaller { return env.client.WithKB(kb).WithHTTPTimeout(toolCallTimeout) },
		CloseCallers: func(kb string) surveyor.ToolCaller { return env.client.WithKB(kb).WithHTTPTimeout(closeTimeout) },
		Models:       env.models,
		Idle:         beater,
		Heartbeat:    beater,
	})
	if err != nil {
		log.Printf("surveyor: %v", err)
		return surveyorExitStart
	}
	beater.Surveyor = r.Replica()
	if env.settings != nil {
		env.settings.Surveyor = r.Replica()
	}
	beater.Start(ctx)
	log.Printf("surveyor %q (replica %s): started for %d KB(s)", env.cfg.Name, r.Replica(), len(env.cfg.KBs))
	return r.Run(ctx, once)
}

// cmdSurveyor runs `cartographer surveyor --config <file>`: the third role of the
// binary, a background process for one perimeter. `serve` never starts it.
func cmdSurveyor(args []string) int {
	if len(args) > 0 && args[0] == "source" {
		return cmdSurveyorSource(args[1:])
	}
	fs := flag.NewFlagSet("surveyor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configFlag := fs.String("config", "", "Path to the Surveyor YAML config (required)")
	onceFlag := fs.Bool("once", false, "Evaluate the triggers once, run if due, and exit")
	logFileFlag := fs.String("log-file", "", "Append the log to this file instead of stderr (created if absent; never rotated)")
	if err := fs.Parse(args); err != nil {
		return surveyorExitStart
	}
	if *configFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: cartographer surveyor --config <file> [--once] [--log-file <file>]\n       cartographer surveyor source test --config <file> ... (try a recipe before deploying it)")
		fs.PrintDefaults()
		return surveyorExitStart
	}
	if *logFileFlag != "" {
		f, err := redirectServerLog(*logFileFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return surveyorExitStart
		}
		defer f.Close()
	}

	// The first signal cancels the run (the closing entries are still written);
	// a second one exits at once.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		stop()
		<-sigs
		log.Print("surveyor: second signal, exiting now")
		os.Exit(surveyorExitRuntime)
	}()
	if ev := watchShutdownEvent(); ev != nil {
		go func() {
			select {
			case <-ev:
				stop()
			case <-ctx.Done():
			}
		}()
	}

	env, err := surveyorStartup(ctx, *configFlag, surveyor.DefaultKinds(), defaultRetry, log.Writer())
	if err != nil {
		if ctx.Err() != nil {
			return 0 // interrupted while waiting for the server: a graceful stop
		}
		log.Printf("surveyor: %v", err)
		var se *startError
		if errors.As(err, &se) {
			return se.code
		}
		return surveyorExitStart
	}
	return surveyorRunFn(ctx, env, *onceFlag)
}

// surveyorStartup runs the start-up checks in order; each failure starts nothing.
// The token and the API keys never reach an error or a log line: they are only
// handed to the clients.
func surveyorStartup(ctx context.Context, path string, kinds *surveyor.Kinds, retry retryPolicy, out io.Writer) (*surveyorEnv, error) {
	cfg, err := surveyor.LoadConfig(path, kinds)
	if err != nil {
		return nil, startFail(surveyorExitStart, "%v", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(out, "surveyor: warning: %s\n", w)
	}
	// The pinned digest is the only consent: a plugin that does not match stops
	// the start, before any model client or network call.
	if err := cfg.LoadPlugins(core.Options{RunningVersion: version, CheckScripts: star.Check}); err != nil {
		return nil, startFail(surveyorExitStart, "surveyor config %s: %v", path, err)
	}
	for _, p := range cfg.Plugins {
		fmt.Fprintf(out, "surveyor: plugin %s %s %s\n", p.Manifest.Name, p.Manifest.Version, p.Digest[:12])
	}

	models := map[string]surveyor.Model{}
	for _, p := range cfg.Models {
		m, err := buildModel(p)
		if err != nil {
			return nil, startFail(surveyorExitStart, "model profile %q: %v", p.Name, err)
		}
		models[p.Name] = m
	}

	c := client.New(cfg.ServerURL, cfg.Token()).WithInsecureHTTP(cfg.AllowInsecureHTTP).WithTokenEnv(cfg.TokenEnv)
	h, err := healthWithRetry(ctx, c, retry)
	if err != nil {
		return nil, err
	}
	// An invalid token never makes Health fail (a public path, served as an
	// anonymous caller: 200 with no kbs), so the token is checked by Ping.
	if err := c.Ping(startupCallTimeout); err != nil {
		return nil, startFail(surveyorExitStart, "the server at %s did not accept the token: %v", cfg.ServerURL, err)
	}
	// A server that names its component protocol is checked before the first
	// beat; one that does not predates the field and is handled by the beat's 404.
	if h.ComponentProtocol > 0 && !componentproto.Supports(componentproto.Protocol, h.ComponentProtocol) {
		return nil, startFail(surveyorExitStart, "the server at %s speaks component protocol %d (serves from %d) and this Surveyor speaks %d: upgrade the one that is behind",
			cfg.ServerURL, h.ComponentProtocol, h.ComponentMinProtocol, componentproto.Protocol)
	}
	if h.KBs == nil {
		return nil, startFail(surveyorExitStart, "the server names no KB for the token in $%s (or is not a multi-KB server)", cfg.TokenEnv)
	}
	visible := map[string]bool{}
	for _, k := range *h.KBs {
		visible[k.Name] = true
	}
	var missing []string
	for _, k := range cfg.KBs {
		if !visible[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, startFail(surveyorExitStart, "the token in $%s does not see KB(s) %s", cfg.TokenEnv, strings.Join(missing, ", "))
	}
	env := &surveyorEnv{cfg: cfg, client: c, models: models}
	// The report is made from the file the surveyor loaded; it carries no secret
	// (D1259), and a failure to make it only means the Atlas shows none.
	if raw, err := os.ReadFile(path); err == nil {
		if rep, err := surveyorconfig.Build(raw, cfg.Name, "", version, cfg.KBs, os.Getenv); err == nil {
			env.settings = rep
		}
	}
	return env, nil
}

func buildModel(p surveyor.Profile) (surveyor.Model, error) {
	opts := []surveyor.Option{surveyor.WithAllowInsecureHTTP(p.AllowInsecureHTTP), surveyor.WithTimeout(time.Duration(p.TimeoutSeconds) * time.Second)}
	switch p.Provider {
	case surveyor.ProviderAnthropic:
		return surveyor.NewAnthropic(p.BaseURL, p.APIKey(), p.Model, opts...)
	case surveyor.ProviderOpenAI:
		return surveyor.NewOpenAI(p.BaseURL, p.APIKey(), p.Model, opts...)
	}
	return nil, fmt.Errorf("unknown provider %q", p.Provider)
}

// healthWithRetry asks /health until the server answers. Only a server that is
// still starting, or unreachable, is retried; the rest is a start error.
func healthWithRetry(ctx context.Context, c *client.MCPClient, r retryPolicy) (*client.Health, error) {
	deadline := time.Now().Add(r.total)
	delay := r.first
	for {
		h, err := c.Health(startupCallTimeout)
		if err == nil {
			return h, nil
		}
		var re *client.RemoteError
		retriable := errors.As(err, &re) && (re.Code == client.CodeStarting || re.Code == client.CodeUnreachable || re.Code == client.CodeDNSFailed || re.HTTPStatus >= 500)
		// A 5xx is a proxy or a rollout in front of the server: Health reports it as CodeHTTPFailed.
		if !retriable {
			return nil, startFail(surveyorExitStart, "health check of %s: %v", c.ServerURL, err)
		}
		if !time.Now().Add(delay).Before(deadline) {
			return nil, startFail(surveyorExitRuntime, "the server at %s is not ready after %s: %v", c.ServerURL, r.total, err)
		}
		if !r.sleep(ctx, delay) {
			return nil, ctx.Err()
		}
		delay = min(delay*2, r.max)
	}
}
