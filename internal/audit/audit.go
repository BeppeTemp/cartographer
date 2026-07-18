package audit

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

const maxArgsLen = 1024

// Entry represents a single audit log entry.
type Entry struct {
	Timestamp  time.Time `json:"timestamp"`
	Tool       string    `json:"tool"`
	Args       string    `json:"args"`
	AgentID    string    `json:"agent_id"`
	Outcome    string    `json:"outcome"`
	DurationMs int64     `json:"duration_ms"`
	CommitSHA  string    `json:"commit_sha,omitempty"`
	PrevHash   string    `json:"prev_hash"`
	Hash       string    `json:"hash"`
	Sig        string    `json:"sig,omitempty"` // hex-encoded Ed25519 signature of Hash (omitempty = unsigned entries)
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
	pub := priv.Public().(ed25519.PublicKey)
	return KeyPair{Private: priv, Public: pub}, nil
}

// SeedToHex returns the hex-encoded seed (32 bytes = 64 hex chars) for backup/restore.
func (kp KeyPair) SeedToHex() string {
	return hex.EncodeToString(kp.Private.Seed())
}

// Log is an append-only audit log backed by a JSONL file.
type Log struct {
	mu       sync.Mutex
	path     string
	lastHash string   // hash of the last written entry; "genesis" when log is empty
	kp       *KeyPair // optional key pair for Ed25519 signing; nil = signing disabled
}

// computeHash returns the sha256 hex of Timestamp|Tool|Args|AgentID|Outcome|PrevHash.
func computeHash(e Entry) string {
	s := e.Timestamp.UTC().Format(time.RFC3339Nano) + "|" +
		e.Tool + "|" +
		e.Args + "|" +
		e.AgentID + "|" +
		e.Outcome + "|" +
		e.PrevHash
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// OpenWithKey opens an audit log and enables Ed25519 signing of each entry.
func OpenWithKey(path string, kp KeyPair) (*Log, error) {
	l, err := Open(path)
	if err != nil {
		return nil, err
	}
	l.kp = &kp
	return l, nil
}

// Open opens or creates an audit log at the given file path.
// If the file already contains entries, the hash-chain is continued from the last entry.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()

	l := &Log{path: path, lastHash: "genesis"}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("audit: parse entry: %w", err)
		}
		l.lastHash = e.Hash
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("audit: scan %s: %w", path, err)
	}

	return l, nil
}

// Append adds an entry to the log, computing the hash-chain.
// Args longer than 1024 characters are truncated.
func (l *Log) Append(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(e.Args) > maxArgsLen {
		e.Args = e.Args[:maxArgsLen]
	}
	e.Timestamp = e.Timestamp.UTC()
	e.PrevHash = l.lastHash
	e.Hash = computeHash(e)

	if l.kp != nil {
		sig := ed25519.Sign(l.kp.Private, []byte(e.Hash))
		e.Sig = hex.EncodeToString(sig)
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("audit: marshal entry: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("audit: open for append %s: %w", l.path, err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "%s\n", data); err != nil {
		return fmt.Errorf("audit: write entry: %w", err)
	}

	l.lastHash = e.Hash
	return nil
}

// readAll reads all entries from the file. Must be called with l.mu held.
func (l *Log) readAll() ([]Entry, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: open %s: %w", l.path, err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("audit: parse entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("audit: scan %s: %w", l.path, err)
	}
	return entries, nil
}

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

// Verify checks the hash-chain integrity of the log.
// Returns the index of the first broken entry, or -1 if the chain is valid.
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

// VerifyFull checks hash-chain integrity and Ed25519 signatures.
// Returns:
//   - count: number of entries with a verified valid signature
//   - unsigned: number of entries without a Sig field
//   - err: first chain/signature error encountered, nil if all pass
func (l *Log) VerifyFull() (count int, unsigned int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, readErr := l.readAll()
	if readErr != nil {
		return 0, 0, readErr
	}

	prevHash := "genesis"
	for i, e := range entries {
		if e.PrevHash != prevHash {
			return count, unsigned, fmt.Errorf("audit: hash chain broken at entry %d", i)
		}
		if e.Hash != computeHash(e) {
			return count, unsigned, fmt.Errorf("audit: hash mismatch at entry %d", i)
		}
		if e.Sig == "" {
			unsigned++
		} else if l.kp != nil {
			sigBytes, decErr := hex.DecodeString(e.Sig)
			if decErr != nil || !ed25519.Verify(l.kp.Public, []byte(e.Hash), sigBytes) {
				return count, unsigned, fmt.Errorf("audit: entry %d: invalid signature", i)
			}
			count++
		} else {
			// Sig present but no key pair to verify with — treat as unsigned.
			unsigned++
		}
		prevHash = e.Hash
	}
	return count, unsigned, nil
}

// Count returns the total number of entries in the log.
func (l *Log) Count() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.readAll()
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
