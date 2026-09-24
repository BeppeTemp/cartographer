package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// The server reports its own staleness (D254) for the clients that ignore
// hook output (Kiro, Antigravity) and for a remote server whose operator is
// not at any client machine. It reports versions only, never a command.

var (
	// serverUpdateDelay keeps a restart loop from hammering GitHub: the first
	// check waits, and the 24 h cache absorbs every restart after it.
	serverUpdateDelay    = 30 * time.Second
	serverUpdateInterval = updatecheck.TTL
	// serverUpdateRetry is the wait after a failed check (GitHub unreachable,
	// rate-limited): a failure writes no cache entry, so waiting the full
	// interval would leave latest_version out for a day (#415).
	serverUpdateRetry   = time.Hour
	serverUpdateCheckFn = func(ctx context.Context, current string, opts updatecheck.Options) (updatecheck.Result, error) {
		return updatecheck.Check(ctx, current, opts)
	}
)

// serverUpdateWatch holds the newer release the last check found.
type serverUpdateWatch struct {
	latest atomic.Value // string
}

func (w *serverUpdateWatch) Latest() string {
	v, _ := w.latest.Load().(string)
	return v
}

// startServerUpdateCheck starts the one checker goroutine and returns the
// source /health and kb_status read, or nil when no check runs: disabled by
// configuration, or a dev build, which is on no release line.
func startServerUpdateCheck(cfg *config.Config, current string) func() string {
	if !cfg.UpdateCheck || current == "" || current == "dev" || updatecheck.DisabledByEnv(nil) {
		return nil
	}
	w := &serverUpdateWatch{}
	opts := updatecheck.Options{CacheFile: serverUpdateCacheFile(cfg)}
	check, delay, interval, retry := serverUpdateCheckFn, serverUpdateDelay, serverUpdateInterval, serverUpdateRetry
	go func() {
		time.Sleep(delay)
		reported := ""
		for {
			// Fail silent: an unknown answer only leaves latest_version out.
			res, err := check(context.Background(), current, opts)
			if res.Available {
				w.latest.Store(res.Latest)
				if res.Latest != reported {
					log.Printf("a newer Cartographer release is available: %s (serving %s)", res.Latest, current)
					reported = res.Latest
				}
			} else {
				w.latest.Store("")
			}
			if err != nil {
				time.Sleep(retry)
			} else {
				time.Sleep(interval)
			}
		}
	}()
	return w.Latest
}

// serverUpdateCacheFile is the server-owned cache: under the data dir's
// .cartographer/ (dot-directories are never discovered as KBs), else the
// process cache dir, never the client's own cache file.
func serverUpdateCacheFile(cfg *config.Config) string {
	if cfg.Data != "" {
		return filepath.Join(cfg.Data, ".cartographer", updatecheck.CacheFileName)
	}
	if dir, err := updatecheck.CacheDir(); err == nil {
		return filepath.Join(dir, "server-"+updatecheck.CacheFileName)
	}
	return filepath.Join(os.TempDir(), "cartographer-server-"+updatecheck.CacheFileName)
}
