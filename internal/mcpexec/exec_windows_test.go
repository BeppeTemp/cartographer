//go:build windows

package mcpexec

import (
	"context"
	"os"
	"testing"
	"time"
)

// An interrupt (the context ending) stops the child: Windows cannot signal it,
// so it is killed, and Exec does not wait for it to finish by itself.
func TestRunKillsTheChildOnCancel(t *testing.T) {
	l := Launch{Command: "ping", Args: []string{"-n", "60", "127.0.0.1"}, Env: os.Environ()}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()
	start := time.Now()
	code, _ := l.run(ctx)
	if time.Since(start) > 20*time.Second {
		t.Fatalf("the child outlived the cancel (%v)", time.Since(start))
	}
	if code == 0 {
		t.Fatal("a killed child reported success")
	}
}
