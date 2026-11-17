package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// #2189: a concept at the admitted size limit must not make a search run for
// minutes, on either backend: one megabyte of one repeated character, and
// ordinary words with the match everywhere.
func TestSearchLargeConceptAnswersInBoundedTime(t *testing.T) {
	t.Parallel()
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		dir := filepath.Join(k.DataRoot(), "manutenzione")
		words := strings.Repeat("lorem ipsum dolor needle sit amet ", 30000)[:900000]
		for name, body := range map[string]string{"big-x": strings.Repeat("x", 1<<20), "big-words": words} {
			doc := "---\ntype: Note\n---\n" + body + "\n"
			if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{"xxxx", "needle"} {
			done := make(chan string, 1)
			go func() { done <- searchText(t, s, q) }()
			select {
			case out := <-done:
				// The in-memory index matches whole words: only "needle" is found there.
				if q == "needle" && !strings.Contains(out, "big-words") {
					t.Fatalf("%q: %s", q, out)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("search %q: no answer in 10s", q)
			}
		}
	})
}

// A request whose context is already cancelled does no search work.
func TestSearchHonoursCancelledContext(t *testing.T) {
	t.Parallel()
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		ctx, cancel := context.WithCancel(authLocalContext())
		cancel()
		args, _ := json.Marshal(map[string]string{"query": "needle"})
		start := time.Now()
		tr := s.callTool(ctx, "search", args)
		if time.Since(start) > 5*time.Second {
			t.Fatalf("cancelled search took %v", time.Since(start))
		}
		if !tr.IsError {
			t.Fatalf("a cancelled search answered: %+v", tr.Content)
		}
	})
}
