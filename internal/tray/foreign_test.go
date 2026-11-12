package tray

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// A program that is not a Cartographer server on the server address is never
// Running: the icon is Stopped and the status line names the address (#1998).
func TestProbeHealthForeignListener(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name    string
		addr    string
		foreign bool
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"1.2.3","ready":true}`))
		}), false},
		{"ours, bootstrapping", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"1.2.3","bootstrapping":true}`))
		}), false},
		{"a 200 that says ok", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), true},
		{"a 200 that is not json", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`hello`)) }), true},
		{"a 404", serve(http.NotFound), true},
		{"nothing", "127.0.0.1:1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := probeHealth(tc.addr)
			if got := errors.Is(err, ErrForeignListener); got != tc.foreign {
				t.Fatalf("foreign = %v, want %v (err %v)", got, tc.foreign, err)
			}
			if tc.foreign && h != nil {
				t.Errorf("a foreign answer returned a Health: %+v", h)
			}
			snap := Classify(h, err, service.Status{Installed: true, HTTPAddr: tc.addr})
			if tc.foreign {
				if snap.State != StateStopped || snap.Foreign != tc.addr {
					t.Errorf("snapshot = %+v, want Stopped with Foreign=%s", snap, tc.addr)
				}
			} else if snap.Foreign != "" {
				t.Errorf("Foreign = %q on a server that is not foreign", snap.Foreign)
			}
		})
	}
}

func TestClassifyForeignIsStoppedEvenWhenNothingIsInstalled(t *testing.T) {
	snap := Classify(nil, ErrForeignListener, service.Status{HTTPAddr: "127.0.0.1:39273"})
	if snap.State != StateStopped || snap.Foreign != "127.0.0.1:39273" {
		t.Errorf("snapshot = %+v", snap)
	}
	if got := StatusLine(snap); got != "another program answers on 127.0.0.1:39273" {
		t.Errorf("StatusLine = %q", got)
	}
	if head := InfoItems(snap)[0]; head.Badge != "Port in use" || head.Tone != ToneDown {
		t.Errorf("head = %+v", head)
	}
	plain := Classify(nil, errors.New("refused"), service.Status{Installed: true})
	if plain.Foreign != "" || StatusLine(plain) != "stopped" {
		t.Errorf("a plain stop = %+v / %q", plain, StatusLine(plain))
	}
}

func TestForeignCheckLeavesOtherErrorsAlone(t *testing.T) {
	starting := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeStarting}
	if _, err := foreignCheck(nil, starting); errors.Is(err, ErrForeignListener) || err != error(starting) {
		t.Errorf("a bootstrapping server became %v", err)
	}
	refused := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnreachable}
	if _, err := foreignCheck(nil, refused); errors.Is(err, ErrForeignListener) {
		t.Error("a refused connection read as foreign")
	}
}

// Client mode reads the same shape: a 200 JSON without a version is a program
// on the server address, never Running (#1998).
func TestProbeClientForeignListener(t *testing.T) {
	foreignSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok","kbs":[{"name":"kb-a"}],"ready":true}`))
	}))
	defer foreignSrv.Close()
	target := ClientTarget{Present: true, ServerURL: foreignSrv.URL + "/mcp"}
	h, err := probeClient(target)
	if !errors.Is(err, ErrForeignListener) || h != nil {
		t.Fatalf("probeClient = %+v, %v, want ErrForeignListener", h, err)
	}
	snap := ClassifyClient(h, err, target)
	addr := strings.TrimPrefix(foreignSrv.URL, "http://")
	if snap.State != StateStopped || snap.Foreign != addr || snap.Version != "" || snap.KBCount != -1 {
		t.Errorf("snapshot = %+v, want Stopped, Foreign=%s", snap, addr)
	}
	if got := StatusLine(snap); got != "another program answers on "+addr {
		t.Errorf("StatusLine = %q", got)
	}
	if head := InfoItems(snap)[0]; head.Badge != "Port in use" || head.Tone != ToneDown {
		t.Errorf("head = %+v", head)
	}

	ours := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok","version":"1.2.3","ready":true}`))
	}))
	defer ours.Close()
	target = ClientTarget{Present: true, ServerURL: ours.URL + "/mcp"}
	h, err = probeClient(target)
	if got := ClassifyClient(h, err, target); err != nil || got.State != StateRunning || got.Foreign != "" {
		t.Errorf("a Cartographer server = %+v (%v)", got, err)
	}

	refused := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnreachable}
	down := ClassifyClient(nil, refused, ClientTarget{Present: true, ServerURL: "https://example.com:8443/mcp"})
	if down.Foreign != "" || StatusLine(down) != "example.com:8443: unreachable" {
		t.Errorf("unreachable = %+v / %q", down, StatusLine(down))
	}
}

// A foreign listener in client mode is logged as such, once per change, never
// as a refused server_url.
func TestClientSnapshotLogsForeignListenerOnce(t *testing.T) {
	f := &clientEnv{target: ClientTarget{Present: true, ServerURL: "http://127.0.0.1:39273/mcp"}, probeErr: ErrForeignListener}
	d := f.deps()
	cm := &clientMemo{}
	for i := 0; i < 3; i++ {
		s, ok := d.clientSnapshot(cm)
		if !ok || s.Foreign != "127.0.0.1:39273" {
			t.Fatalf("snapshot = %+v, %v", s, ok)
		}
	}
	want := "tray: another program answers on 127.0.0.1:39273"
	if len(f.logs) != 1 || f.logs[0] != want {
		t.Fatalf("logs = %q, want exactly [%q]", f.logs, want)
	}
	f.probeErr = nil
	d.clientSnapshot(cm)
	f.probeErr = ErrForeignListener
	d.clientSnapshot(cm)
	if len(f.logs) != 2 {
		t.Errorf("logs after a passing probe = %q, want the line again", f.logs)
	}
}
