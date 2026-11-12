package main

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/wsl"
)

func TestCmdWsl_RefusesOutsideWSL(t *testing.T) {
	orig := wslDetectFn
	wslDetectFn = func() wsl.Info { return wsl.Info{} }
	t.Cleanup(func() { wslDetectFn = orig })
	var code int
	errOut := withStderr(t, func() { code = cmdWsl([]string{"keepalive"}) })
	if code != exitStatusError || !strings.Contains(errOut, "only inside a WSL distro") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	errOut = withStderr(t, func() { code = cmdWsl(nil) })
	if code != exitStatusError || !strings.Contains(errOut, "usage") {
		t.Errorf("no action: code %d, stderr %q", code, errOut)
	}
}

// The loop runs ensure at start and on every tick, survives an ensure error and
// returns on a signal.
func TestRunKeepAlive(t *testing.T) {
	stop := make(chan os.Signal, 1)
	tick := make(chan time.Time)
	calls := make(chan struct{}, 8)
	ensure := func() error {
		calls <- struct{}{}
		return errors.New("crontab unavailable")
	}
	done := make(chan struct{})
	var errOut string
	go func() {
		errOut = withStderr(t, func() { runKeepAlive(stop, tick, ensure) })
		close(done)
	}()
	<-calls // at start
	tick <- time.Now()
	<-calls // after an error, still looping
	tick <- time.Now()
	<-calls
	stop <- syscall.SIGTERM
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the keep-alive did not return on SIGTERM")
	}
	if strings.Count(errOut, "keepalive: crontab unavailable") != 3 {
		t.Errorf("stderr = %q", errOut)
	}
}
