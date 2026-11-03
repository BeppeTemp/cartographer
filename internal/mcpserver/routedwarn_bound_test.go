package mcpserver

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
)

// Not parallel: it swaps the global log output.
func TestRoutedWarned_BoundedAndQuiet(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	m := &MultiKBServer{}
	key := func(i int) string { return fmt.Sprintf("kb-one|%s-%d", strings.Repeat("n", 5000), i) }
	for i := 0; i < 500; i++ {
		m.routedMu.Lock()
		m.warnRoutedOnceLocked(key(i), "routed mount: unknown %s", cutBytes(key(i), routedWarnNameMax))
		m.routedMu.Unlock()
	}
	if n := len(m.routedWarned); n > routedWarnedMax {
		t.Fatalf("%d entries, want at most %d", n, routedWarnedMax)
	}
	if lines := strings.Count(buf.String(), "\n"); lines > routedWarnedMax+1 {
		t.Fatalf("%d log lines, want at most %d", lines, routedWarnedMax+1)
	}
	if m.routedWarnSuppressed != 500-routedWarnedMax {
		t.Fatalf("suppressed = %d", m.routedWarnSuppressed)
	}
	for _, l := range strings.Split(buf.String(), "\n") {
		if len(l) > 500 {
			t.Fatalf("log line of %d bytes", len(l))
		}
	}
	before := buf.Len()
	m.routedMu.Lock()
	m.warnRoutedOnceLocked(key(0), "again")
	m.routedMu.Unlock()
	if buf.Len() != before {
		t.Fatal("repeat key logged again")
	}
}
