//go:build !windows

package service

// The keep-alive's Ensure step needs the cron fakes, which are unix-only.

import (
	"os"
	"testing"
)

func TestKeepAliveEnsure(t *testing.T) {
	t.Run("cron installed starts the server", func(t *testing.T) {
		e := newCronEnv(t)
		e.install(t)
		e.m.Stop()
		os.Remove(e.state("server.stopped")) // what a reboot leaves: no process, no stop marker
		before := len(e.procs.starts)
		if err := e.m.KeepAliveEnsure(); err != nil {
			t.Fatal(err)
		}
		if len(e.procs.starts) != before+1 {
			t.Errorf("Ensure did not start the server: %d -> %d", before, len(e.procs.starts))
		}
	})
	t.Run("nothing installed does nothing", func(t *testing.T) {
		e := newCronEnv(t)
		if err := e.m.KeepAliveEnsure(); err != nil || len(e.procs.starts) != 0 {
			t.Errorf("err %v, started %v", err, e.procs.starts)
		}
	})
	t.Run("systemd does nothing", func(t *testing.T) {
		h := newWslHost(t, wslInfo())
		if _, err := h.m.Install(InstallOptions{ConfigPath: h.cfg}); err != nil {
			t.Fatal(err)
		}
		n := len(h.stub.calls)
		if err := h.m.KeepAliveEnsure(); err != nil || len(h.stub.calls) != n {
			t.Errorf("err %v, calls %v", err, h.stub.calls[n:])
		}
	})
}
