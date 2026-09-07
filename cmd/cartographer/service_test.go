package main

import (
	"encoding/json"
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

// TestPrintServiceStatus covers what the D174 rework is about: never a health
// verdict on a service that does not exist, never a claim of a live process,
// and the one context line that explains a remote client.
func TestPrintServiceStatus(t *testing.T) {
	cases := []struct {
		name      string
		st        service.Status
		serverURL string
		want      []string
		absent    []string
	}{
		{
			name:   "not installed",
			st:     service.Status{Lifecycle: service.LifecycleNotInstalled, HealthSkipReason: service.HealthSkipNoConfig},
			want:   []string{"installed: false", "no local cartographer service is installed", "cartographer service install"},
			absent: []string{"healthy:", "health:", "loaded:"},
		},
		{
			name:   "installed with an http address",
			st:     service.Status{Installed: true, Running: true, Lifecycle: service.LifecycleLoaded, HealthChecked: true, Healthy: true, HTTPAddr: "127.0.0.1:39273"},
			want:   []string{"installed: true", "loaded:    true", "healthy:   true (http 127.0.0.1:39273)"},
			absent: []string{"running:", "not checked"},
		},
		{
			name:   "installed, stdio transport",
			st:     service.Status{Installed: true, Running: true, Lifecycle: service.LifecycleLoaded, HealthSkipReason: service.HealthSkipStdio},
			want:   []string{"health:    not checked", "stdio transport"},
			absent: []string{"healthy:"},
		},
		{
			name:   "installed, config unreadable",
			st:     service.Status{Installed: true, Lifecycle: service.LifecycleNotLoaded, HealthSkipReason: service.HealthSkipUnreadableConfig},
			want:   []string{"loaded:    false", "health:    not checked", "could not be read"},
			absent: []string{"healthy:"},
		},
		{
			name:      "remote client is context, not a warning",
			st:        service.Status{Installed: true, Running: true, Lifecycle: service.LifecycleLoaded, HealthChecked: true},
			serverURL: "https://cartographer.example.com/mcp",
			want:      []string{"client:  configured against https://cartographer.example.com/mcp"},
			absent:    []string{"warning", "Error"},
		},
		{
			name:      "loopback client gets no context line",
			st:        service.Status{Installed: true, Running: true, Lifecycle: service.LifecycleLoaded, HealthChecked: true},
			serverURL: "http://127.0.0.1:39273/mcp",
			absent:    []string{"client:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			printServiceStatus(&sb, tc.st, tc.serverURL)
			got := sb.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("output should not contain %q:\n%s", a, got)
				}
			}
		})
	}
}

// TestServiceSnapshotContract: `service status --output json` is consumed by
// scripts. New fields are fine, renamed or re-meaning ones are not.
func TestServiceSnapshotContract(t *testing.T) {
	st := service.Status{Installed: true, Running: true, Healthy: true, HTTPAddr: ":39273", Lifecycle: service.LifecycleLoaded, HealthChecked: true}
	b, err := json.Marshal(newServiceSnapshot(st))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"installed":      true,
		"running":        true,
		"healthy":        true,
		"http_addr":      ":39273",
		"lifecycle":      "loaded",
		"health_checked": true,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	// A skipped check must be visible next to the bool it invalidates.
	b, _ = json.Marshal(newServiceSnapshot(service.Status{Lifecycle: service.LifecycleNotInstalled, HealthSkipReason: service.HealthSkipStdio}))
	got = map[string]any{}
	json.Unmarshal(b, &got)
	if got["health_checked"] != false || got["health_skip_reason"] != "stdio_transport" {
		t.Errorf("skipped check snapshot = %s", b)
	}
}
