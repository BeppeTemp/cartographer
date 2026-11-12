package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Wake returns a channel that receives (without ever blocking an append) after
// each successful append: a reader of the chain, such as the audit sink, waits
// on it instead of polling the file. The channel has a buffer of one, so a
// burst of appends wakes the reader once.
func (l *Log) Wake() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan struct{}, 1)
	l.wake = append(l.wake, ch)
	return ch
}

// signalLocked wakes every reader; a full buffer already holds a wake-up.
func (l *Log) signalLocked() {
	for _, ch := range l.wake {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// afterSnapshot is what EventsAfter takes under the append mutex: the
// checkpoint index, the active segment's size and the last hash. Nothing is
// read from disk under the mutex.
type afterSnapshot struct {
	segments []Checkpoint
	dir      string
	size     int64
	lastHash string
}

func (l *Log) snapshotAfter() (afterSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fi, err := os.Stat(l.path)
	if err != nil && !os.IsNotExist(err) {
		return afterSnapshot{}, fmt.Errorf("audit: stat active segment: %w", err)
	}
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	return afterSnapshot{
		segments: append([]Checkpoint(nil), l.index.Segments...),
		dir:      archiveDir(l.path, l.opts.ArchiveDir),
		size:     size,
		lastHash: l.lastHash,
	}, nil
}

// EventsAfter returns, in chain order, at most limit entries that follow the
// entry whose hash is cursor, across the retained segments and the active
// file. An empty cursor starts at the oldest retained entry. When entries
// after the cursor are gone (their segment was deleted by retention, known only
// to the checkpoint index), or the cursor is not in the chain at all, gap is
// true and the result starts at the first entry still retained after the
// point it can place the cursor: the end of a deleted segment whose first or
// last hash it is, else the oldest retained entry. Placing it costs a read of
// the retained segments up to the one that holds it.
//
// It never writes, and holds the append mutex only to snapshot the index and
// the active file's size; the files are read after. A rotation that happens
// during the read is detected (the last entry read is not the snapshot's) and
// the read starts over.
func (l *Log) EventsAfter(cursor string, limit int) (events []Entry, gap bool, err error) {
	if limit <= 0 {
		return nil, false, nil
	}
	changed := errRotating // the reason the last attempt was read again
	for attempt := 0; attempt < 3; attempt++ {
		snap, err := l.snapshotAfter()
		if err != nil {
			return nil, false, err
		}
		active, err := readPrefix(l.path, snap.size)
		if errors.Is(err, errSegmentChanged) {
			changed = err // the active file was replaced by a rotation meanwhile
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if len(active) > 0 && active[len(active)-1].Hash != snap.lastHash {
			changed = errRotating // rotated between the snapshot and the read
			continue
		}
		events, gap, err := eventsAfter(snap, active, cursor, limit)
		if errors.Is(err, errSegmentChanged) {
			changed = errRotating
			continue
		}
		return events, gap, err
	}
	// Still changed after the retries: the last attempt's reason, which names
	// the file when the active one does not parse.
	return nil, false, changed
}

var (
	errSegmentChanged = errors.New("audit: segment changed during read")
	errRotating       = errors.New("audit: the log kept rotating while it was read")
)

// eventsAfter walks the timeline: each checkpointed segment (its entries when
// the file is retained, nothing when it is not), then the active entries.
func eventsAfter(snap afterSnapshot, active []Entry, cursor string, limit int) ([]Entry, bool, error) {
	// The cursor in the active file, or at the boundary right before it, needs
	// no segment read: the common case of a sink that keeps up.
	if cursor != "" {
		if i := indexOfHash(active, cursor); i >= 0 {
			return clip(active[i+1:], limit), false, nil
		}
		if n := len(snap.segments); n > 0 && snap.segments[n-1].LastHash == cursor {
			return clip(active, limit), false, nil
		}
	}
	found := cursor == ""
	gap := false
	var out []Entry
	take := func(es []Entry) bool {
		for _, e := range es {
			if out = append(out, e); len(out) == limit {
				return true
			}
		}
		return false
	}
	// read keeps each segment read before the cursor was placed, so a cursor
	// that is nowhere replays from them without reading them again.
	var read [][]Entry
	for _, cp := range snap.segments {
		entries, err := readSegment(filepath.Join(snap.dir, cp.Name), cp)
		if err != nil {
			return nil, false, err
		}
		switch {
		case !found && entries == nil:
			// Deleted by retention: only its first and last hashes are known.
			if cp.LastHash == cursor {
				found = true
			} else if cp.FirstHash == cursor {
				found, gap = true, true
			}
		case !found:
			read = append(read, entries)
			if i := indexOfHash(entries, cursor); i >= 0 {
				found = true
				if take(entries[i+1:]) {
					return out, gap, nil
				}
			}
		case entries == nil:
			if cursor != "" {
				gap = true // retention removed entries the reader has not had
			}
		default:
			if take(entries) {
				return out, gap, nil
			}
		}
	}
	if !found {
		// The cursor is not in the retained chain: its segment was deleted,
		// or it is not a hash of this log. Replay from the oldest retained
		// entry (at least once) and say so.
		out, gap = nil, true
		for _, entries := range read {
			if take(entries) {
				return out, gap, nil
			}
		}
	}
	take(active)
	return out, gap, nil
}

// readSegment reads a rotated segment; nil, no error when retention removed it.
// A segment whose last hash is not its checkpoint's is one being written
// right now by an interrupted rotation's repair: the caller reads again.
func readSegment(path string, cp Checkpoint) ([]Entry, error) {
	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, nil
	}
	if entries[len(entries)-1].Hash != cp.LastHash {
		return nil, errSegmentChanged
	}
	return entries, nil
}

// readPrefix reads the entries of the first size bytes of path: what the
// active file held at the snapshot, whatever an append adds meanwhile.
func readPrefix(path string, size int64) ([]Entry, error) {
	if size == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, size))
	if err != nil {
		return nil, fmt.Errorf("audit: read %s: %w", path, err)
	}
	// An append writes whole lines under the mutex, so the snapshot's size
	// ends a line: a cut elsewhere is another file (a rotation replaced it).
	if int64(len(data)) < size || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("%w: %s does not end a line at the snapshot", errSegmentChanged, path)
	}
	var out []Entry
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%w: parse %s: %v", errSegmentChanged, path, err)
		}
		out = append(out, e)
	}
	return out, nil
}

func indexOfHash(es []Entry, hash string) int {
	for i := range es {
		if es[i].Hash == hash {
			return i
		}
	}
	return -1
}

func clip(es []Entry, limit int) []Entry {
	if len(es) > limit {
		return es[:limit]
	}
	return es
}
