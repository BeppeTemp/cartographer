package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const syncEventScript = `def done(ctx):
    log.info("fired " + ctx.event)

on("sync.after", done)
`

// lockProbe is a run's error stream. A handler's log line lands here while the
// handler runs, so taking the client lock from Write proves it is free then.
type lockProbe struct {
	dir   string
	buf   bytes.Buffer
	free  int
	taken error
}

func (p *lockProbe) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "fired") {
		release, err := provisioning.LockClientState(p.dir, 300*time.Millisecond)
		if err != nil {
			p.taken = err
		} else {
			_ = release()
			p.free++
		}
	}
	return p.buf.Write(b)
}

func syncWithPlugin(t *testing.T, kbs []string, opts syncOptions) (*lockProbe, error) {
	t.Helper()
	home := installScripted(t, "team-ev", syncEventScript, true)
	name := "solo"
	if len(kbs) > 1 {
		name = "shared" // the same artifact from two KBs: a collision, as TestSyncFailsBeforeMaterializing
	}
	srv := prefixAwareMCPServer(t, map[string]string{kbs[0]: "", kbs[len(kbs)-1]: ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, name)
	})
	t.Cleanup(srv.Close)
	cfg := mustCfg(t, home)
	cfg.ServerURL, cfg.ServerName, cfg.Agents, cfg.KnownKBs = srv.URL+"/mcp", "cartographer", []string{"claude"}, kbs
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	probe := &lockProbe{dir: home}
	opts.Streams = syncStreams{Out: &bytes.Buffer{}, Err: probe}
	_, err := runSync(home, cfg, opts)
	return probe, err
}

func TestSyncAfterRunsOutsideTheClientLock(t *testing.T) {
	probe, err := syncWithPlugin(t, []string{"one"}, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if probe.free != 1 || probe.taken != nil {
		t.Errorf("the handler ran %d times with the lock free, lock error %v\n%s", probe.free, probe.taken, probe.buf.String())
	}
}

func TestSyncAfterFiresForNoDryRunNoLockedCallerNoEarlyError(t *testing.T) {
	probe, err := syncWithPlugin(t, []string{"one"}, syncOptions{DryRun: true})
	if err != nil || strings.Contains(probe.buf.String(), "fired") {
		t.Errorf("dry run: err %v\n%s", err, probe.buf.String())
	}
	// A caller that holds the lock itself passes no Unlock.
	probe, err = syncWithPlugin(t, []string{"one"}, syncOptions{Locked: true})
	if err != nil || strings.Contains(probe.buf.String(), "fired") {
		t.Errorf("locked caller: err %v\n%s", err, probe.buf.String())
	}
	// Two KBs serving one artifact: the run fails before the apply.
	probe, err = syncWithPlugin(t, []string{"one", "two"}, syncOptions{})
	if err == nil || strings.Contains(probe.buf.String(), "fired") {
		t.Errorf("early error: err %v\n%s", err, probe.buf.String())
	}
}
