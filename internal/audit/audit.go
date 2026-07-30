// Package audit implements Cartographer's local, tamper-evident audit trail.
//
// Version 0 (legacy) entries are deliberately kept readable: installations
// that already have an audit log can upgrade without breaking the hash chain
// they have accumulated so far. New writes (D119) use version 2, whose hash
// is computed over a canonical JSON encoding of the redacted, versioned event
// rather than the legacy concatenated-string formula.
//
// This package is local, tamper-evident storage: an append-only hash chain
// with optional Ed25519 signatures, checkpointed rotation, and best-effort or
// fail-closed retry semantics. It is not an external immutable/WORM store and
// makes no legal compliance claim — see docs/deployment.md.
package audit

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxArgsLen bounds the legacy (pre-D119) free-form Args field. Retained
	// only for the deprecated Append/Entry.Args compatibility path.
	maxArgsLen = 1024

	// VersionV2 marks a redacted, versioned event (D119). Version 0 (the zero
	// value, omitted from JSON) marks a legacy pre-D119 entry.
	VersionV2 = 2

	PhaseAttempt    = "attempt"
	PhaseCompletion = "completion"

	OutcomeSuccess          = "success"
	OutcomeApplicationErr   = "application_error"
	OutcomeInternalErr      = "internal_error"
	OutcomeUnauthenticated  = "unauthenticated"
	OutcomeUnauthorized     = "unauthorized"
	OutcomeCancelled        = "cancelled"
	OutcomeAuditUnavailable = "audit_unavailable"
	OutcomeUnknownTool      = "unknown_tool"
)

// Entry is both the legacy (pre-D119) schema and the versioned D119 audit
// event. Args and AgentID are legacy-only: new server code must use
// AppendEvent, never the deprecated Append.
type Entry struct {
	Version      int       `json:"version,omitempty"`
	ID           string    `json:"id,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	Phase        string    `json:"phase,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	PrincipalID  string    `json:"principal_id,omitempty"`
	Transport    string    `json:"transport,omitempty"`
	KB           string    `json:"kb,omitempty"`
	Tool         string    `json:"tool"`
	ExternalTool string    `json:"external_tool,omitempty"`
	ReadOnly     bool      `json:"read_only,omitempty"`
	// Resources holds only allow-listed identifiers (concept ID, map/journal,
	// artifact kind/name, asset path, ...) — never frontmatter/body, patch
	// values, secret names/values, or raw request JSON. See
	// internal/mcpserver/audit.go's auditResourceFields.
	Resources map[string]string `json:"resources,omitempty"`
	// Args and AgentID are populated only by legacy (pre-D119) callers of
	// Append. They remain exported so offline consumers of old logs still see
	// them; a D119 event always leaves both empty.
	Args       string `json:"args,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	PrevHash   string `json:"prev_hash"`
	Hash       string `json:"hash"`
	Sig        string `json:"sig,omitempty"` // hex-encoded Ed25519 signature of Hash (omitempty = unsigned entries)
}

// KeyPair holds an Ed25519 key pair for signing audit entries.
type KeyPair struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// GenerateKeyPair generates a new Ed25519 key pair.
func GenerateKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("audit: generate key pair: %w", err)
	}
	return KeyPair{Private: priv, Public: pub}, nil
}

// KeyPairFromSeed creates a KeyPair from a 32-byte seed (hex-encoded, 64 hex chars).
func KeyPairFromSeed(hexSeed string) (KeyPair, error) {
	seed, err := hex.DecodeString(hexSeed)
	if err != nil {
		return KeyPair{}, fmt.Errorf("audit: decode seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("audit: seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return KeyPair{Private: priv, Public: priv.Public().(ed25519.PublicKey)}, nil
}

// SeedToHex returns the hex-encoded seed (32 bytes = 64 hex chars) for backup/restore.
func (kp KeyPair) SeedToHex() string { return hex.EncodeToString(kp.Private.Seed()) }

// Options controls rotation, retention, and failure handling. Zero values
// retain the pre-D119 behaviour: one file, no rotation, best-effort mode.
type Options struct {
	// Mode is "best_effort" (default) or "required". In required mode a
	// failed attempt-phase append rejects the MCP call before dispatch
	// (fail closed); in best_effort mode a failed append is dropped and
	// counted, but the call proceeds (backward compatible).
	Mode string
	// MaxBytes/MaxAge, if either is positive, trigger rotation of the active
	// segment to the archive directory once crossed.
	MaxBytes int64
	MaxAge   time.Duration
	// RetentionDays, if positive, deletes a rotated segment once it is older
	// than this many days AND its checkpoint has already been durably
	// written — never before, and never the active segment.
	RetentionDays int
	// ArchiveDir overrides the default "<log-dir>/audit-archive" directory
	// that holds rotated segments and the checkpoint index.
	ArchiveDir string
	// QueueCapacity bounds concurrent append admission (overload becomes
	// visible rather than an unbounded goroutine/memory growth). Appends are
	// deliberately serialized and drained synchronously (never reordered);
	// the bound simply makes overload observable and, in required mode, fail
	// closed.
	QueueCapacity int
	// RetryAttempts/RetryBackoff bound the bounded retry-with-backoff loop
	// around each durable append, absorbing a transient write failure
	// (e.g. a momentary EBUSY/EINTR) without reordering queued events.
	RetryAttempts int
	RetryBackoff  time.Duration
}

// State reports the sink's current operational health, exposed via /health,
// /ready, and `cartographer status`.
type State struct {
	Mode          string `json:"mode"`
	Healthy       bool   `json:"healthy"`
	Ready         bool   `json:"ready"` // false only when Mode=="required" and unhealthy
	Dropped       uint64 `json:"dropped_events"`
	QueueDepth    int    `json:"queue_depth"`
	QueueCapacity int    `json:"queue_capacity"`
	Error         string `json:"error,omitempty"`
}

// Checkpoint is durable evidence of one closed (rotated) segment, written
// before the segment can ever be deleted by retention. PrevHash is the chain
// boundary immediately before the segment's first record, so a later-deleted
// segment's place in the hash chain remains provable from the checkpoint
// alone (first_hash/last_hash/count), even once its raw entries are gone.
type Checkpoint struct {
	Name            string    `json:"name"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	Count           int       `json:"count"`
	PrevHash        string    `json:"prev_hash"`
	FirstHash       string    `json:"first_hash"`
	LastHash        string    `json:"last_hash"`
	SigningRequired bool      `json:"signing_required,omitempty"`
}

type checkpointIndex struct {
	Version  int          `json:"version"`
	Segments []Checkpoint `json:"segments"`
	// Sig signs the checkpointBytes encoding (Version+Segments only), so the
	// whole index — including deleted-segment evidence — is tamper-evident
	// even after the segments themselves are gone.
	Sig string `json:"sig,omitempty"`
}

func archiveDir(path, configured string) string {
	if configured != "" {
		return configured
	}
	return filepath.Join(filepath.Dir(path), "audit-archive")
}

func checkpointPath(path, configured string) string {
	return filepath.Join(archiveDir(path, configured), "checkpoints.json")
}

func checkpointBytes(index checkpointIndex) []byte {
	b, _ := json.Marshal(struct {
		Version  int          `json:"version"`
		Segments []Checkpoint `json:"segments"`
	}{index.Version, index.Segments})
	return b
}

func loadCheckpointIndex(path string) (checkpointIndex, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return checkpointIndex{Version: 1}, nil
	}
	if err != nil {
		return checkpointIndex{}, fmt.Errorf("audit: read checkpoint index: %w", err)
	}
	var idx checkpointIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return checkpointIndex{}, fmt.Errorf("audit: parse checkpoint index: %w", err)
	}
	if idx.Version != 1 {
		return checkpointIndex{}, fmt.Errorf("audit: unknown checkpoint index version %d", idx.Version)
	}
	return idx, nil
}

// writeAllFunc performs the actual write(2) loop for a durable append. It is
// a package-level variable so tests can inject a write failure partway
// through a multi-chunk write (fault injection for the append-rollback path)
// without needing a real full disk.
var writeAllFunc = writeAll

func writeAll(f *os.File, b []byte) error {
	for len(b) > 0 {
		n, err := f.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-*")
	if err != nil {
		return fmt.Errorf("audit: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("audit: chmod temp file: %w", err)
	}
	if err := writeAll(tmp, data); err != nil {
		tmp.Close()
		return fmt.Errorf("audit: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("audit: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("audit: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("audit: rename temp file: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		defer dir.Close()
		_ = dir.Sync() // best-effort directory-entry durability; POSIX-portable failure is not fatal
	}
	return nil
}

// Log is an append-only audit log backed by a JSONL file, with optional
// rotation, checkpointed retention, and Ed25519 signing. All methods
// serialize through mu, so concurrent dispatches always produce one valid
// chain with correctly paired request IDs.
type Log struct {
	mu             sync.Mutex
	path, lastHash string
	kp             *KeyPair // optional key pair for Ed25519 signing; nil = signing disabled
	opts           Options
	opened         time.Time // start time of the active segment (for MaxAge rotation)
	healthy        bool
	dropped        uint64
	lastErr        error
	sequence       uint64
	queueDepth     int
	index          checkpointIndex
}

func normalizeOptions(o Options) Options {
	o.Mode = strings.ToLower(strings.TrimSpace(o.Mode))
	if o.Mode == "" {
		o.Mode = "best_effort"
	}
	if o.Mode != "best_effort" && o.Mode != "required" {
		o.Mode = "required" // fail closed on an unrecognized mode rather than silently degrading
	}
	if o.QueueCapacity <= 0 {
		o.QueueCapacity = 256
	}
	if o.RetryAttempts <= 0 {
		o.RetryAttempts = 3
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = 20 * time.Millisecond
	}
	return o
}

// Open opens or creates an audit log at the given file path, with default
// Options (one file, best-effort, no rotation) — byte-identical to the
// pre-D119 behaviour.
func Open(path string) (*Log, error) { return OpenWithOptions(path, Options{}) }

// OpenWithKey opens an audit log and enables Ed25519 signing of each entry.
func OpenWithKey(path string, kp KeyPair) (*Log, error) {
	return OpenWithKeyAndOptions(path, kp, Options{})
}

// OpenWithOptions opens (or creates) an audit log with explicit rotation/
// retention/queue/retry Options and no signing.
func OpenWithOptions(path string, opts Options) (*Log, error) { return open(path, nil, opts) }

// OpenWithKeyAndOptions opens (or creates) a signed audit log with explicit
// Options.
func OpenWithKeyAndOptions(path string, kp KeyPair, opts Options) (*Log, error) {
	return open(path, &kp, opts)
}

func open(path string, kp *KeyPair, opts Options) (*Log, error) {
	if path == "" {
		return nil, errors.New("audit: empty log path")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	f.Close()

	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}

	opts = normalizeOptions(opts)
	l := &Log{path: path, lastHash: "genesis", kp: kp, opts: opts, opened: time.Now().UTC(), healthy: true}

	idx, err := loadCheckpointIndex(checkpointPath(path, opts.ArchiveDir))
	if err != nil {
		return nil, err
	}
	l.index = idx
	l.sequence = uint64(len(idx.Segments))

	if len(entries) > 0 {
		l.lastHash = entries[len(entries)-1].Hash
		l.opened = entries[0].Timestamp
	} else if len(idx.Segments) > 0 {
		l.lastHash = idx.Segments[len(idx.Segments)-1].LastHash
	}
	return l, nil
}

// legacyHash reproduces the pre-D119 (version 0) hash formula exactly, so
// existing installations keep a verifiable chain across the upgrade.
func legacyHash(e Entry) string {
	s := e.Timestamp.UTC().Format(time.RFC3339Nano) + "|" +
		e.Tool + "|" +
		e.Args + "|" +
		e.AgentID + "|" +
		e.Outcome + "|" +
		e.PrevHash
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// canonicalEntry is the exact, field-ordered JSON encoding hashed for a
// version-2 event — deliberately explicit (not a direct json.Marshal of
// Entry) so a future field addition to Entry cannot silently change the hash
// of every existing v2 record.
type canonicalEntry struct {
	Version      int               `json:"version"`
	ID           string            `json:"id"`
	Timestamp    string            `json:"timestamp"`
	Phase        string            `json:"phase"`
	RequestID    string            `json:"request_id"`
	PrincipalID  string            `json:"principal_id"`
	Transport    string            `json:"transport"`
	KB           string            `json:"kb"`
	Tool         string            `json:"tool"`
	ExternalTool string            `json:"external_tool,omitempty"`
	ReadOnly     bool              `json:"read_only"`
	Resources    map[string]string `json:"resources,omitempty"`
	Outcome      string            `json:"outcome"`
	DurationMs   int64             `json:"duration_ms"`
	CommitSHA    string            `json:"commit_sha,omitempty"`
	PrevHash     string            `json:"prev_hash"`
}

func computeHash(e Entry) string {
	if e.Version == 0 {
		return legacyHash(e)
	}
	b, _ := json.Marshal(canonicalEntry{
		e.Version, e.ID, e.Timestamp.UTC().Format(time.RFC3339Nano), e.Phase, e.RequestID,
		e.PrincipalID, e.Transport, e.KB, e.Tool, e.ExternalTool, e.ReadOnly, e.Resources,
		e.Outcome, e.DurationMs, e.CommitSHA, e.PrevHash,
	})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validPhase(v string) bool { return v == PhaseAttempt || v == PhaseCompletion }

func validOutcome(v string) bool {
	switch v {
	case OutcomeSuccess, OutcomeApplicationErr, OutcomeInternalErr, OutcomeUnauthenticated,
		OutcomeUnauthorized, OutcomeCancelled, OutcomeAuditUnavailable, OutcomeUnknownTool:
		return true
	}
	return false
}

// Append adds a legacy (pre-D119, unversioned) entry to the log. Retained for
// offline/compatibility use; new server code must call AppendEvent, which
// emits a redacted, versioned (D119) event instead. Args longer than 1024
// characters are truncated (legacy behaviour).
func (l *Log) Append(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(e.Args) > maxArgsLen {
		e.Args = e.Args[:maxArgsLen]
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	return l.appendLocked(&e)
}

// AppendEvent appends a D119 versioned, redacted event: an attempt or
// completion phase of one tool call, correlated by RequestID. It validates
// the event shape (phase/outcome/tool/request ID) before ever touching disk,
// stamps Version/ID/Timestamp, and strips the legacy free-form fields so a
// D119 event never carries unredacted arguments.
func (l *Log) AppendEvent(e Entry) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Version = VersionV2
	if e.ID == "" {
		e.ID = newID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.RequestID == "" {
		return Entry{}, errors.New("audit: request ID is required")
	}
	if !validPhase(e.Phase) {
		return Entry{}, fmt.Errorf("audit: invalid phase %q", e.Phase)
	}
	// The outcome of a call is only known once it completes: an attempt
	// phase must not carry one, and a completion phase must carry a valid
	// one from the fixed taxonomy.
	if e.Phase == PhaseCompletion {
		if !validOutcome(e.Outcome) {
			return Entry{}, fmt.Errorf("audit: invalid outcome %q", e.Outcome)
		}
	} else if e.Outcome != "" {
		return Entry{}, errors.New("audit: attempt phase must not set an outcome")
	}
	if e.Tool == "" {
		return Entry{}, errors.New("audit: tool is required")
	}
	e.Args, e.AgentID = "", "" // never permit the legacy free-form fields in a D119 event
	err := l.appendLocked(&e)
	return e, err
}

func (l *Log) appendLocked(e *Entry) error {
	if l.queueDepth >= l.opts.QueueCapacity {
		return l.failedLocked(errors.New("audit: queue full"))
	}
	l.queueDepth++
	defer func() { l.queueDepth-- }()

	if err := l.rotateLocked(); err != nil {
		return l.failedLocked(err)
	}

	e.Timestamp = e.Timestamp.UTC()
	e.PrevHash = l.lastHash
	e.Hash = computeHash(*e)
	if l.kp != nil {
		e.Sig = hex.EncodeToString(ed25519.Sign(l.kp.Private, []byte(e.Hash)))
	}

	b, err := json.Marshal(e)
	if err != nil {
		return l.failedLocked(fmt.Errorf("audit: marshal entry: %w", err))
	}

	var appendErr error
	for attempt := 0; attempt < l.opts.RetryAttempts; attempt++ {
		appendErr = appendDurable(l.path, append(b, '\n'))
		if appendErr == nil {
			break
		}
		if attempt+1 < l.opts.RetryAttempts {
			time.Sleep(l.opts.RetryBackoff)
		}
	}
	if appendErr != nil {
		return l.failedLocked(fmt.Errorf("audit: append %s: %w", l.path, appendErr))
	}

	l.lastHash = e.Hash
	l.healthy = true
	l.lastErr = nil
	return nil
}

// appendDurable appends data to path with fsync, and rolls back (truncates)
// to the pre-write offset on a partial write so a failed append never leaves
// a truncated, unparseable trailing line for the next reader/writer.
func appendDurable(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if err := writeAllFunc(f, data); err != nil {
		// Roll back a partial write: never leave a truncated, unparseable
		// trailing line for the next Append/read to trip over.
		_ = f.Truncate(offset)
		_ = f.Sync()
		return err
	}
	return f.Sync()
}

func (l *Log) failedLocked(err error) error {
	l.healthy = false
	l.lastErr = err
	if l.opts.Mode == "best_effort" {
		l.dropped++
	}
	return err
}

// rotateLocked closes the active segment once MaxBytes/MaxAge is crossed,
// writes a signed checkpoint entry for it, and opens a fresh empty active
// segment.
//
// Ordering is deliberate and is what makes retention safe: the checkpoint
// index is written — durably, atomically — BEFORE the active segment is
// renamed into the archive or truncated. If the checkpoint write fails (disk
// full, permission loss, ...), rotateLocked returns an error and the active
// file is never touched: no data is renamed, truncated, or lost, and the
// index gains no entry for a segment that doesn't durably exist yet. Only
// once the checkpoint has landed does the segment get renamed out of the
// active path and the active path get truncated for reuse. Retention
// (retainLocked) only ever considers files that are both present in the
// archive directory AND listed in this already-durable index, so a segment
// can never be deleted before its checkpoint is durable.
func (l *Log) rotateLocked() error {
	if l.opts.MaxBytes <= 0 && l.opts.MaxAge <= 0 {
		return nil
	}
	fi, err := os.Stat(l.path)
	if err != nil {
		return fmt.Errorf("audit: stat active segment: %w", err)
	}
	if fi.Size() == 0 {
		return nil
	}
	crossed := (l.opts.MaxBytes > 0 && fi.Size() >= l.opts.MaxBytes) ||
		(l.opts.MaxAge > 0 && time.Since(l.opened) >= l.opts.MaxAge)
	if !crossed {
		return nil
	}

	dir := archiveDir(l.path, l.opts.ArchiveDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("audit: create archive dir: %w", err)
	}
	entries, err := readEntries(l.path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	nextSeq := l.sequence + 1
	name := fmt.Sprintf("%s.%06d.jsonl", time.Now().UTC().Format("20060102T150405.000000000Z"), nextSeq)
	segmentPath := filepath.Join(dir, name)

	cp := Checkpoint{
		Name: name, Start: entries[0].Timestamp.UTC(), End: entries[len(entries)-1].Timestamp.UTC(),
		Count: len(entries), PrevHash: entries[0].PrevHash, FirstHash: entries[0].Hash,
		LastHash: entries[len(entries)-1].Hash, SigningRequired: l.kp != nil,
	}
	candidateIndex := checkpointIndex{Version: 1, Segments: append(append([]Checkpoint{}, l.index.Segments...), cp)}
	if l.kp != nil {
		candidateIndex.Sig = hex.EncodeToString(ed25519.Sign(l.kp.Private, checkpointBytes(candidateIndex)))
	}
	idxData, err := json.Marshal(candidateIndex)
	if err != nil {
		return fmt.Errorf("audit: marshal checkpoint index: %w", err)
	}
	// Durable, atomic checkpoint write BEFORE the segment is renamed out of
	// the active path: see the rotateLocked doc comment above.
	if err := atomicWrite(checkpointPath(l.path, l.opts.ArchiveDir), idxData, 0o640); err != nil {
		return fmt.Errorf("audit: checkpoint: %w", err)
	}
	l.index = candidateIndex
	l.sequence = nextSeq

	if err := os.Rename(l.path, segmentPath); err != nil {
		// The checkpoint now (rarely) refers to a segment that failed to
		// land at its archive path; the source data itself is untouched by
		// a failed rename (POSIX rename is all-or-nothing), so nothing is
		// lost — but the operator must reconcile manually. Surfaced via the
		// returned error and (in required mode) via /health, /ready.
		return fmt.Errorf("audit: rotate rename (checkpoint already durable, manual reconciliation required): %w", err)
	}

	active, err := os.OpenFile(l.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("audit: create rotated active log: %w", err)
	}
	if err := active.Sync(); err != nil {
		active.Close()
		return fmt.Errorf("audit: fsync rotated active log: %w", err)
	}
	if err := active.Close(); err != nil {
		return fmt.Errorf("audit: close rotated active log: %w", err)
	}
	l.opened = time.Now().UTC()

	l.retainLocked(dir)
	return nil
}

// retainLocked deletes rotated segments older than RetentionDays, but only
// those that already have a checkpoint entry recorded (checkpointed) — a
// segment can never be deleted before its checkpoint is durable, and the
// active segment is never a candidate (it never lives in dir under its
// rotated name).
func (l *Log) retainLocked(dir string) {
	if l.opts.RetentionDays <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.opts.RetentionDays)
	checkpointed := make(map[string]bool, len(l.index.Segments))
	for _, cp := range l.index.Segments {
		checkpointed[cp.Name] = true
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !checkpointed[e.Name()] {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func readEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var out []Entry
	for line := 1; s.Scan(); line++ {
		if len(strings.TrimSpace(s.Text())) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("audit: parse %s line %d: %w", path, line, err)
		}
		out = append(out, e)
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("audit: scan %s: %w", path, err)
	}
	return out, nil
}

func (l *Log) readAll() ([]Entry, error) { return readEntries(l.path) }

// Tail returns the last n entries in reverse chronological order (newest first).
func (l *Log) Tail(n int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}
	if n > len(entries) {
		n = len(entries)
	}
	result := make([]Entry, n)
	for i := 0; i < n; i++ {
		result[i] = entries[len(entries)-1-i]
	}
	return result, nil
}

// Count returns the total number of entries in the active segment.
func (l *Log) Count() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := l.readAll()
	return len(entries), err
}

// Summary is the result of an offline chain/signature verification pass.
type Summary struct {
	Valid          int // entries whose hash chain (and signature, if checked) validated
	Unsigned       int // entries with no Sig field
	Incomplete     int // v2 attempt phases with no matching completion
	Retained       int // rotated segments whose file was present and verified
	CheckpointOnly int // rotated segments known only via the checkpoint index (file deleted by retention)
	Corrupt        int // entries/segments that failed validation
	Events         []Entry
}

// VerifyFile validates one file (legacy or v2 entries, or a mix — a log can
// be upgraded in place).
func VerifyFile(path string, public ed25519.PublicKey) (Summary, error) {
	return VerifyFiles([]string{path}, public)
}

// VerifyFiles validates ordered segments as one continuous hash chain. A
// rotated segment's first entry deliberately points at the preceding
// segment's final hash, so segments must be verified together, in order.
func VerifyFiles(paths []string, public ed25519.PublicKey) (Summary, error) {
	var es []Entry
	for _, path := range paths {
		part, err := readEntries(path)
		if err != nil {
			return Summary{}, err
		}
		es = append(es, part...)
	}
	sum := Summary{Events: es}
	prev := "genesis"
	ids := map[string]bool{}
	attempts := map[string]bool{}
	for i, e := range es {
		if e.Timestamp.IsZero() {
			sum.Corrupt++
			return sum, fmt.Errorf("audit: entry %d: malformed timestamp", i)
		}
		if e.PrevHash != prev || e.Hash != computeHash(e) {
			sum.Corrupt++
			return sum, fmt.Errorf("audit: hash chain broken at entry %d", i)
		}
		if e.Version != 0 && e.Version != VersionV2 {
			sum.Corrupt++
			return sum, fmt.Errorf("audit: unknown version %d at entry %d", e.Version, i)
		}
		if e.Version == VersionV2 {
			if e.ID == "" || ids[e.ID] || !validPhase(e.Phase) || !validV2Outcome(e) {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: malformed v2 event at entry %d", i)
			}
			ids[e.ID] = true
			if e.Phase == PhaseAttempt {
				if attempts[e.RequestID] {
					sum.Corrupt++
					return sum, fmt.Errorf("audit: duplicate attempt at entry %d", i)
				}
				attempts[e.RequestID] = true
			} else if !attempts[e.RequestID] {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: completion without attempt at entry %d", i)
			} else {
				delete(attempts, e.RequestID)
			}
		}
		if e.Sig == "" {
			sum.Unsigned++
		} else if public != nil {
			b, err := hex.DecodeString(e.Sig)
			if err != nil || !ed25519.Verify(public, []byte(e.Hash), b) {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: invalid signature at entry %d", i)
			}
		}
		sum.Valid++
		prev = e.Hash
	}
	sum.Incomplete = len(attempts)
	if sum.Incomplete > 0 {
		return sum, fmt.Errorf("audit: %d attempt(s) without completion", sum.Incomplete)
	}
	return sum, nil
}

// validV2Outcome mirrors the phase-aware rule enforced at append time: an
// attempt phase must carry no outcome, a completion phase must carry a valid
// one.
func validV2Outcome(e Entry) bool {
	if e.Phase == PhaseCompletion {
		return validOutcome(e.Outcome)
	}
	return e.Outcome == ""
}

// VerifyAudit validates the durable checkpoint index, retained (still
// present) rotated segments, checkpoint-only (already deleted by retention)
// segments, and the active file — all as one continuous chain. It is the
// offline operator entry point (`cartographer audit verify`/`export`) and
// never modifies anything on disk.
func VerifyAudit(path, configuredArchive string, public ed25519.PublicKey) (Summary, error) {
	if _, err := os.Stat(path); err != nil {
		return Summary{}, fmt.Errorf("audit: active log: %w", err)
	}
	idx, err := loadCheckpointIndex(checkpointPath(path, configuredArchive))
	if err != nil {
		return Summary{}, err
	}
	needsSignature := false
	for _, cp := range idx.Segments {
		needsSignature = needsSignature || cp.SigningRequired
	}
	if needsSignature && (idx.Sig == "" || public == nil) {
		return Summary{}, errors.New("audit: signing-required checkpoint cannot be verified without a public key")
	}
	if idx.Sig != "" && public != nil {
		sig, err := hex.DecodeString(idx.Sig)
		if err != nil || !ed25519.Verify(public, checkpointBytes(idx), sig) {
			return Summary{}, errors.New("audit: invalid checkpoint index signature")
		}
	}

	active, err := readEntries(path)
	if err != nil {
		return Summary{}, err
	}

	// checkpointEntry is a synthetic chain-boundary marker for a
	// checkpoint-only (deleted) segment: its own entries can no longer be
	// re-verified, but the checkpoint still proves the chain continued
	// correctly across the gap.
	type boundary struct {
		isBoundary bool
		prevHash   string
		hash       string
	}
	var timeline []Entry
	var boundaries = map[int]boundary{} // index in timeline -> boundary info

	checkpointOnly, retained := 0, 0
	previousCheckpoint := "genesis"
	for _, cp := range idx.Segments {
		if cp.Name == "" || cp.Count <= 0 || cp.PrevHash != previousCheckpoint || cp.FirstHash == "" || cp.LastHash == "" || cp.End.Before(cp.Start) {
			return Summary{Corrupt: 1}, fmt.Errorf("audit: invalid checkpoint %q", cp.Name)
		}
		segmentPath := filepath.Join(archiveDir(path, configuredArchive), cp.Name)
		if _, statErr := os.Stat(segmentPath); statErr == nil {
			es, readErr := readEntries(segmentPath)
			if readErr != nil {
				return Summary{}, readErr
			}
			if len(es) != cp.Count || es[0].PrevHash != cp.PrevHash || es[0].Hash != cp.FirstHash || es[len(es)-1].Hash != cp.LastHash {
				return Summary{Corrupt: 1}, fmt.Errorf("audit: checkpoint mismatch for retained segment %q", cp.Name)
			}
			timeline = append(timeline, es...)
			retained++
		} else if os.IsNotExist(statErr) {
			boundaries[len(timeline)] = boundary{isBoundary: true, prevHash: cp.PrevHash, hash: cp.LastHash}
			timeline = append(timeline, Entry{}) // placeholder consumed via boundaries[i]
			checkpointOnly++
		} else {
			return Summary{}, statErr
		}
		previousCheckpoint = cp.LastHash
	}
	activeStart := len(timeline)
	timeline = append(timeline, active...)

	sum := Summary{Retained: retained, CheckpointOnly: checkpointOnly}
	ids := map[string]bool{}
	attempts := map[string]bool{}
	prev := "genesis"
	for i, e := range timeline {
		if b, ok := boundaries[i]; ok {
			if b.prevHash != prev {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: checkpoint boundary mismatch at segment %d", i)
			}
			prev = b.hash
			sum.Valid++
			continue
		}
		if e.Timestamp.IsZero() || e.PrevHash != prev || e.Hash != computeHash(e) {
			sum.Corrupt++
			return sum, fmt.Errorf("audit: chain broken at entry %d", i)
		}
		if e.Version != 0 && e.Version != VersionV2 {
			sum.Corrupt++
			return sum, fmt.Errorf("audit: unknown version %d at entry %d", e.Version, i)
		}
		if e.Version == VersionV2 {
			if e.ID == "" || ids[e.ID] || !validPhase(e.Phase) || !validV2Outcome(e) {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: malformed v2 event at entry %d", i)
			}
			ids[e.ID] = true
			if e.Phase == PhaseAttempt {
				if attempts[e.RequestID] {
					sum.Corrupt++
					return sum, fmt.Errorf("audit: duplicate attempt at entry %d", i)
				}
				attempts[e.RequestID] = true
			} else if attempts[e.RequestID] {
				delete(attempts, e.RequestID)
			} else if i >= activeStart || checkpointOnly == 0 {
				// A completion with no matching attempt is only tolerated
				// when it might legitimately belong to a checkpoint-only
				// (deleted) segment whose paired attempt is no longer
				// readable; inside the active/retained timeline it is
				// always an error.
				sum.Corrupt++
				return sum, fmt.Errorf("audit: completion without attempt at entry %d", i)
			}
			if needsSignature && e.Sig == "" {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: unsigned event in signing-required segment at entry %d", i)
			}
		}
		if e.Sig == "" {
			sum.Unsigned++
		} else if public != nil {
			b, err := hex.DecodeString(e.Sig)
			if err != nil || !ed25519.Verify(public, []byte(e.Hash), b) {
				sum.Corrupt++
				return sum, fmt.Errorf("audit: invalid signature at entry %d", i)
			}
		}
		sum.Valid++
		sum.Events = append(sum.Events, e)
		prev = e.Hash
	}
	sum.Incomplete = len(attempts)
	if sum.Incomplete > 0 {
		return sum, fmt.Errorf("audit: %d attempt(s) without completion", sum.Incomplete)
	}
	return sum, nil
}

// Verify checks the hash-chain integrity of the active segment only.
// Returns the index of the first broken entry, or -1 if the chain is valid.
// For the full picture across rotation/retention, use VerifyAudit.
func (l *Log) Verify() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.readAll()
	if err != nil {
		return -1, err
	}
	prevHash := "genesis"
	for i, e := range entries {
		if e.PrevHash != prevHash {
			return i, nil
		}
		if e.Hash != computeHash(e) {
			return i, nil
		}
		if e.Sig != "" && l.kp != nil {
			sigBytes, err := hex.DecodeString(e.Sig)
			if err != nil || !ed25519.Verify(l.kp.Public, []byte(e.Hash), sigBytes) {
				return i, fmt.Errorf("audit: entry %d: invalid signature", i)
			}
		}
		prevHash = e.Hash
	}
	return -1, nil
}

// VerifyFull checks hash-chain integrity and Ed25519 signatures of the active
// segment. Returns the count of signed-and-valid entries, the count of
// unsigned entries, and the first chain/signature error (nil if all pass).
func (l *Log) VerifyFull() (count int, unsigned int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var public ed25519.PublicKey
	if l.kp != nil {
		public = l.kp.Public
	}
	sum, sumErr := VerifyFile(l.path, public)
	return sum.Valid - sum.Unsigned, sum.Unsigned, sumErr
}

// State returns the sink's current operational health.
func (l *Log) State() State {
	l.mu.Lock()
	defer l.mu.Unlock()
	ready := l.opts.Mode != "required" || l.healthy
	st := State{Mode: l.opts.Mode, Healthy: l.healthy, Ready: ready, Dropped: l.dropped, QueueDepth: l.queueDepth, QueueCapacity: l.opts.QueueCapacity}
	if l.lastErr != nil {
		st.Error = l.lastErr.Error()
	}
	return st
}

// Required reports whether the log is configured in fail-closed mode.
func (l *Log) Required() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.opts.Mode == "required" }

// Path returns the active segment's file path.
func (l *Log) Path() string { return l.path }

// Close is the graceful-shutdown flush point. Every append is already
// synchronously durable (fsync'd before returning), so there is never a
// pending write to flush; Close only guards the invariant that the bounded
// admission counter is back at zero.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.queueDepth != 0 {
		return errors.New("audit: queue not drained")
	}
	return nil
}

// Segments returns every archived (rotated, still-present) segment file path
// in chronological order, followed by the active file path. Used by the
// offline verify/export commands; it never mutates anything.
func Segments(path, archiveDir string) ([]string, error) {
	if archiveDir == "" {
		archiveDir = filepath.Join(filepath.Dir(path), "audit-archive")
	}
	var out []string
	entries, err := os.ReadDir(archiveDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, filepath.Join(archiveDir, e.Name()))
		}
	}
	sort.Strings(out)
	return append(out, path), nil
}

// ModeName reports the configured failure mode ("best_effort" or "required"),
// so startup logs can state which one is active instead of leaving an operator
// to infer it from configuration.
func (l *Log) ModeName() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.Mode == "" {
		return "best_effort"
	}
	return l.opts.Mode
}

// FailAppendsForTest makes every subsequent durable append fail, and returns a
// function restoring the real writer. It exists so callers outside this package
// (the MCP audit layer, whose whole contract is what happens when auditing
// fails) can exercise the failure path without reimplementing the sink.
func FailAppendsForTest() func() {
	orig := writeAllFunc
	writeAllFunc = func(*os.File, []byte) error { return errors.New("audit: injected write failure") }
	return func() { writeAllFunc = orig }
}
