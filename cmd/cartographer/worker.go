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
	"github.com/BeppeTemp/cartographer/internal/worker"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

// Exit codes of `cartographer worker`: 0 graceful shutdown, 1 runtime failure,
// 2 start error (nothing was started).
const (
	workerExitRuntime = 1
	workerExitStart   = 2
)

// startError is a failure before the worker started, with its exit code.
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

// workerEnv is what a successful start-up hands to the runner.
type workerEnv struct {
	cfg    *worker.Config
	client *client.MCPClient
	models map[string]worker.Model // by profile name
	// settings is the report posted to the server (typed entries, no secret).
	settings *workerconfig.Report
}

// closeTimeout bounds each closing log_append on shutdown; the entries are
// written in parallel, so the total stays well inside Kubernetes' default 30 s
// terminationGracePeriodSeconds.
const closeTimeout = 25 * time.Second

// toolCallTimeout bounds one tool call of a task. The client's default 30 s is
// for interactive use: kb_repair over a whole KB takes over a minute, and a call
// the client abandons keeps running on the server, holding the KB's lock.
const toolCallTimeout = 5 * time.Minute

// workerRunFn is the runner behind the start-up checks (stubbed by tests).
var workerRunFn = func(ctx context.Context, env *workerEnv, once bool) int {
	tiers := map[string]string{}
	for _, p := range env.cfg.Models {
		tiers[p.Name] = p.Tier
	}
	beater := &worker.Beater{API: env.client, Perimeter: env.cfg.Name, Version: version, KBs: env.cfg.KBs,
		Tiers: tiers, Settings: env.settings, Logf: log.Printf}
	r, err := worker.NewRunner(env.cfg, worker.DefaultKinds(), worker.RunnerOptions{
		Callers:      func(kb string) worker.ToolCaller { return env.client.WithKB(kb).WithHTTPTimeout(toolCallTimeout) },
		CloseCallers: func(kb string) worker.ToolCaller { return env.client.WithKB(kb).WithHTTPTimeout(closeTimeout) },
		Models:       env.models,
		Idle:         beater,
		Heartbeat:    beater,
	})
	if err != nil {
		log.Printf("worker: %v", err)
		return workerExitStart
	}
	beater.Worker = r.Replica()
	if env.settings != nil {
		env.settings.Worker = r.Replica()
	}
	beater.Start(ctx)
	log.Printf("worker %q (replica %s): started for %d KB(s)", env.cfg.Name, r.Replica(), len(env.cfg.KBs))
	return r.Run(ctx, once)
}

// cmdWorker runs `cartographer worker --config <file>`: the third role of the
// binary, a background process for one perimeter. `serve` never starts it.
func cmdWorker(args []string) int {
	if len(args) > 0 && args[0] == "source" {
		return cmdWorkerSource(args[1:])
	}
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configFlag := fs.String("config", "", "Path to the worker YAML config (required)")
	onceFlag := fs.Bool("once", false, "Evaluate the triggers once, run if due, and exit")
	logFileFlag := fs.String("log-file", "", "Append the log to this file instead of stderr (created if absent; never rotated)")
	if err := fs.Parse(args); err != nil {
		return workerExitStart
	}
	if *configFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: cartographer worker --config <file> [--once] [--log-file <file>]\n       cartographer worker source test --config <file> ... (try a recipe before deploying it)")
		fs.PrintDefaults()
		return workerExitStart
	}
	if *logFileFlag != "" {
		f, err := redirectServerLog(*logFileFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return workerExitStart
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
		log.Print("worker: second signal, exiting now")
		os.Exit(workerExitRuntime)
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

	env, err := workerStartup(ctx, *configFlag, worker.DefaultKinds(), defaultRetry, log.Writer())
	if err != nil {
		if ctx.Err() != nil {
			return 0 // interrupted while waiting for the server: a graceful stop
		}
		log.Printf("worker: %v", err)
		var se *startError
		if errors.As(err, &se) {
			return se.code
		}
		return workerExitStart
	}
	return workerRunFn(ctx, env, *onceFlag)
}

// workerStartup runs the start-up checks in order; each failure starts nothing.
// The token and the API keys never reach an error or a log line: they are only
// handed to the clients.
func workerStartup(ctx context.Context, path string, kinds *worker.Kinds, retry retryPolicy, out io.Writer) (*workerEnv, error) {
	cfg, err := worker.LoadConfig(path, kinds)
	if err != nil {
		return nil, startFail(workerExitStart, "%v", err)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(out, "worker: warning: %s\n", w)
	}

	models := map[string]worker.Model{}
	for _, p := range cfg.Models {
		m, err := buildModel(p)
		if err != nil {
			return nil, startFail(workerExitStart, "model profile %q: %v", p.Name, err)
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
		return nil, startFail(workerExitStart, "the server at %s did not accept the token: %v", cfg.ServerURL, err)
	}
	if h.KBs == nil {
		return nil, startFail(workerExitStart, "the server names no KB for the token in $%s (or is not a multi-KB server)", cfg.TokenEnv)
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
		return nil, startFail(workerExitStart, "the token in $%s does not see KB(s) %s", cfg.TokenEnv, strings.Join(missing, ", "))
	}
	env := &workerEnv{cfg: cfg, client: c, models: models}
	// The report is made from the file the worker loaded; it carries no secret
	// (D1259), and a failure to make it only means the Atlas shows none.
	if raw, err := os.ReadFile(path); err == nil {
		if rep, err := workerconfig.Build(raw, cfg.Name, "", version, cfg.KBs, os.Getenv); err == nil {
			env.settings = rep
		}
	}
	return env, nil
}

func buildModel(p worker.Profile) (worker.Model, error) {
	opts := []worker.Option{worker.WithAllowInsecureHTTP(p.AllowInsecureHTTP), worker.WithTimeout(time.Duration(p.TimeoutSeconds) * time.Second)}
	switch p.Provider {
	case worker.ProviderAnthropic:
		return worker.NewAnthropic(p.BaseURL, p.APIKey(), p.Model, opts...)
	case worker.ProviderOpenAI:
		return worker.NewOpenAI(p.BaseURL, p.APIKey(), p.Model, opts...)
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
			return nil, startFail(workerExitStart, "health check of %s: %v", c.ServerURL, err)
		}
		if !time.Now().Add(delay).Before(deadline) {
			return nil, startFail(workerExitRuntime, "the server at %s is not ready after %s: %v", c.ServerURL, r.total, err)
		}
		if !r.sleep(ctx, delay) {
			return nil, ctx.Err()
		}
		delay = min(delay*2, r.max)
	}
}
