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
}

// workerRunFn is the runner behind the start-up checks. The loop that decides
// when to work lands in the next change; until then a started worker only
// waits for its shutdown signal (or returns at once with --once).
var workerRunFn = func(ctx context.Context, env *workerEnv, once bool) int {
	log.Printf("worker %q: started for %d KB(s); no task kind is registered yet", env.cfg.Name, len(env.cfg.KBs))
	if once {
		return 0
	}
	<-ctx.Done()
	return 0
}

// cmdWorker runs `cartographer worker --config <file>`: the third role of the
// binary, a background process for one perimeter. `serve` never starts it.
func cmdWorker(args []string) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configFlag := fs.String("config", "", "Path to the worker YAML config (required)")
	onceFlag := fs.Bool("once", false, "Evaluate the triggers once, run if due, and exit")
	logFileFlag := fs.String("log-file", "", "Append the log to this file instead of stderr (created if absent; never rotated)")
	if err := fs.Parse(args); err != nil {
		return workerExitStart
	}
	if *configFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: cartographer worker --config <file> [--once] [--log-file <file>]")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	return &workerEnv{cfg: cfg, client: c, models: models}, nil
}

func buildModel(p worker.Profile) (worker.Model, error) {
	opt := worker.WithAllowInsecureHTTP(p.AllowInsecureHTTP)
	switch p.Provider {
	case worker.ProviderAnthropic:
		return worker.NewAnthropic(p.BaseURL, p.APIKey(), p.Model, opt)
	case worker.ProviderOpenAI:
		return worker.NewOpenAI(p.BaseURL, p.APIKey(), p.Model, opt)
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
		retriable := errors.As(err, &re) && (re.Code == client.CodeStarting || re.Code == client.CodeUnreachable || re.Code == client.CodeDNSFailed)
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
