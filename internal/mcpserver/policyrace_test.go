package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// D775: a settings refresh replaces the KB's policy while kb_status and lint
// read it. Under -race this failed when the policy was a set of plain fields
// the reload assigned one by one.
func TestPolicyRefreshDoesNotRaceReaders(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	var n int
	var mu sync.Mutex
	k.SettingsReload = func(k *kb.KB) {
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		k.SetPolicy(kb.Policy{
			UsageStaleDays: i % 5,
		})
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	reader := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					f()
				}
			}
		}()
	}
	reader(func() {
		res, err := s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
		if err != nil || res.IsError {
			t.Errorf("kb_status = %+v, %v", res, err)
		}
	})
	reader(func() {
		if _, err := lint.Run(k, "", false); err != nil {
			t.Errorf("lint: %v", err)
		}
	})
	reader(func() { _ = uiAutoChecks(k) })

	path := filepath.Join(k.Root, kb.SettingsFile)
	for i := 0; i < 60; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("usage_stale_days: 30\n# %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i+1) * time.Minute)
		_ = os.Chtimes(path, at, at)
		k.RefreshSettings()
	}
	close(done)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if n == 0 {
		t.Fatal("the reload never ran: the test exercised nothing")
	}
}
