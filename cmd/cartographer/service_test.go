package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/service"
)

func TestServiceHelpUsesStdoutAndSucceeds(t *testing.T) {
	out := withStdout(t, func() {
		if code := cmdService([]string{"--help"}); code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "Usage: cartographer service") {
		t.Fatalf("help = %q", out)
	}
}

func TestServiceWithoutActionFails(t *testing.T) {
	if code := cmdService(nil); code != exitStatusError {
		t.Errorf("exit = %d, want %d", code, exitStatusError)
	}
}

// withServiceRestartStubs stubs serviceRestartFn/serviceReplaceFn for the
// duration of the test, restoring the originals on cleanup.
func withServiceRestartStubs(t *testing.T, restart func() error, replace func(service.ReplaceOptions) error) {
	t.Helper()
	origRestart, origReplace := serviceRestartFn, serviceReplaceFn
	if restart != nil {
		serviceRestartFn = restart
	}
	if replace != nil {
		serviceReplaceFn = replace
	}
	t.Cleanup(func() { serviceRestartFn, serviceReplaceFn = origRestart, origReplace })
}

func TestCmdServiceRestart_PlainIsBackwardCompatible(t *testing.T) {
	var gotRestart, gotReplace bool
	withServiceRestartStubs(t,
		func() error { gotRestart = true; return nil },
		func(service.ReplaceOptions) error { gotReplace = true; return nil },
	)

	out := withStdout(t, func() {
		if code := cmdServiceRestart(nil); code != exitStatusRunning {
			t.Errorf("exit = %d, want %d", code, exitStatusRunning)
		}
	})
	if !gotRestart {
		t.Error("plain restart should call serviceRestartFn")
	}
	if gotReplace {
		t.Error("plain restart must not call serviceReplaceFn (backward compatible)")
	}
	if !strings.Contains(out, "service restarted") {
		t.Errorf("output = %q, want it to report the plain restart", out)
	}
}

func TestCmdServiceRestart_PlainErrorReturnsExitStatusError(t *testing.T) {
	withServiceRestartStubs(t, func() error { return errors.New("boom") }, nil)
	if code := cmdServiceRestart(nil); code != exitStatusError {
		t.Errorf("exit = %d, want %d", code, exitStatusError)
	}
}

func TestCmdServiceRestart_WaitUsesReplaceWithVersionAndConfig(t *testing.T) {
	var gotOpts service.ReplaceOptions
	var gotReplace bool
	withServiceRestartStubs(t,
		func() error { t.Fatal("--wait must not call the plain restart path"); return nil },
		func(opts service.ReplaceOptions) error { gotReplace = true; gotOpts = opts; return nil },
	)

	oldVersion := version
	version = "v1.2.3"
	t.Cleanup(func() { version = oldVersion })

	out := withStdout(t, func() {
		if code := cmdServiceRestart([]string{"--wait", "--config", "/custom/server.yaml"}); code != exitStatusRunning {
			t.Errorf("exit = %d, want %d", code, exitStatusRunning)
		}
	})
	if !gotReplace {
		t.Fatal("--wait should call serviceReplaceFn")
	}
	if gotOpts.ConfigPath != "/custom/server.yaml" {
		t.Errorf("ReplaceOptions.ConfigPath = %q, want /custom/server.yaml", gotOpts.ConfigPath)
	}
	if gotOpts.ExpectedVersion != "v1.2.3" {
		t.Errorf("ReplaceOptions.ExpectedVersion = %q, want v1.2.3", gotOpts.ExpectedVersion)
	}
	if !strings.Contains(out, "v1.2.3") {
		t.Errorf("output = %q, want it to report the verified version", out)
	}
}

func TestCmdServiceRestart_WaitPrintsSuccessOnlyAfterProof(t *testing.T) {
	withServiceRestartStubs(t, nil, func(service.ReplaceOptions) error { return errors.New("timed out waiting for /health") })

	if code := cmdServiceRestart([]string{"--wait"}); code != exitStatusError {
		t.Errorf("exit = %d, want %d", code, exitStatusError)
	}
}
