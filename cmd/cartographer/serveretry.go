package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

// A remote KB whose clone failed at start is skipped, not fatal (#1963), and
// the clone is attempted again with back-off: a network error that clears must
// not keep the KB out until someone restarts the server. The server has no live
// mount (its tables are built once), so a clone that finally succeeds ends the
// server gracefully with restartToMountExit and its supervisor starts it again,
// now with the KB on disk.

// restartToMountExit is the exit status of that restart: non-zero, because
// launchd (KeepAlive) and systemd (Restart=on-failure) restart a failed process only.
const restartToMountExit = 75

var errRestartToMount = errors.New("a skipped KB is cloned: restart to mount it")

// Back-off bounds of the clone retry; variables so a test shortens them.
var (
	cloneRetryFirst = 30 * time.Second
	cloneRetryMax   = 15 * time.Minute
)

// supervisedEnv is the explicit marker that something restarts this process
// when it exits non-zero: the service definitions set it (launchd, systemd) or
// pass --supervised (the Windows task), and an operator whose container has a
// restart policy sets it.
const supervisedEnv = "CARTOGRAPHER_SUPERVISED"

// supervisedProcess reports whether that is so. The marker wins; the rest are
// the heuristics for a service installed before it existed: a systemd unit
// (INVOCATION_ID), a launchd job (XPC_SERVICE_NAME is its label; a terminal has
// "0" or none), Kubernetes. A bare container is not: `docker run` has no
// restart policy unless the operator gave one, and then sets the marker.
func supervisedProcess(getenv func(string) string) bool {
	if v := getenv(supervisedEnv); v != "" {
		return v == "1" || strings.EqualFold(v, "true")
	}
	if getenv("INVOCATION_ID") != "" || getenv("KUBERNETES_SERVICE_HOST") != "" || getenv("K_SERVICE") != "" {
		return true
	}
	v := getenv("XPC_SERVICE_NAME")
	return v != "" && v != "0" && !strings.HasPrefix(v, "application.")
}

// requestRestartToMount ends the server for its supervisor to start it again
// with the cloned KB, or, run by hand, says a restart is needed and goes on:
// exiting would only stop a server nobody restarts.
func requestRestartToMount(name string, supervised bool, restartCh chan<- struct{}) {
	if !supervised {
		log.Printf("KB %q was cloned on retry: restart the server to mount it (it is not run by a service, so it keeps serving the other KBs)", name)
		return
	}
	log.Printf("KB %q was cloned on retry: the server restarts to mount it", name)
	select {
	case restartCh <- struct{}{}:
	default:
	}
}

// cloneRetry is a remote KB whose clone failed at start.
type cloneRetry struct {
	spec config.KBSpec
	name string
}

// skipReason is the client-facing reason of a failed clone: git's remedy for a
// known failure, else a pointer to the log. The error text itself is never
// shown: it names the clone's destination, a path of the server.
func skipReason(err error) string {
	if remedy := gitx.RemoteRemedy(err.Error()); remedy != "" {
		return "the clone failed: " + remedy
	}
	return "the clone failed: the remote did not answer or is not a KB (the server log has the cause)"
}

// retryClones attempts each failed clone again until it succeeds, with a delay
// that doubles up to cloneRetryMax. On the first success it calls restart and
// returns; it also returns when ctx ends.
func retryClones(ctx context.Context, cfg *config.Config, pending []cloneRetry, skipped *mcpserver.SkippedKBs, restart func(name string)) {
	if len(pending) == 0 {
		return
	}
	delay := cloneRetryFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		for _, r := range pending {
			_, err := ensureClonedKBOn(r.spec.Remote, r.name, cfg.Data, r.spec.BranchOverride, gitEnvForKB(r.name, cfg.CredentialsPath())...)
			if err == nil {
				log.Printf("KB %q (remote %s) cloned on retry", r.name, gitx.RedactRemoteURL(r.spec.Remote))
				restart(r.name)
				return
			}
			log.Printf("ERROR: KB %q (remote %s) is still skipped, the clone failed again (next try in %s): %v",
				r.name, gitx.RedactRemoteURL(r.spec.Remote), min(delay*2, cloneRetryMax), err)
			skipped.Set(mcpserver.SkippedKB{Name: r.name, Reason: skipReason(err), Retrying: true})
		}
		delay = min(delay*2, cloneRetryMax)
	}
}
