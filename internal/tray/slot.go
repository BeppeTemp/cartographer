package tray

import "sync"

// Slot keeps the newest snapshot a UI has not applied yet. The poll goroutine
// stores into it; the UI's main thread takes from it whenever it gets to run
// (a dispatched block, applicationDidFinishLaunching, menuNeedsUpdate:), so a
// wake-up that is lost or arrives before the run loop starts costs nothing: the
// snapshot is still there for the next taker (#2183).
type Slot struct {
	mu  sync.Mutex
	sn  Snapshot
	set bool
}

// Put replaces the waiting snapshot, if any, with sn.
func (s *Slot) Put(sn Snapshot) {
	s.mu.Lock()
	s.sn, s.set = sn, true
	s.mu.Unlock()
}

// Take returns the waiting snapshot and empties the slot; ok is false when
// none is waiting.
func (s *Slot) Take() (sn Snapshot, ok bool) {
	s.mu.Lock()
	sn, ok = s.sn, s.set
	s.sn, s.set = Snapshot{}, false
	s.mu.Unlock()
	return sn, ok
}
