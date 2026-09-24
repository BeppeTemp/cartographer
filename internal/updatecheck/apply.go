package updatecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Update policies (`update.policy` in .cartographer.yaml).
const (
	PolicyNotify    = "notify"
	PolicyAutoPatch = "auto-patch"
)

// ValidPolicies lists the accepted `update.policy` values, for error messages.
var ValidPolicies = []string{PolicyNotify, PolicyAutoPatch}

// ParsePolicy normalizes a policy spelling. Empty means the default, notify;
// anything else unknown is an error naming the valid values, so a typo in a
// setting that makes a machine upgrade itself fails loudly.
func ParsePolicy(v string) (string, error) {
	switch p := strings.ToLower(strings.TrimSpace(v)); p {
	case "":
		return PolicyNotify, nil
	case PolicyNotify, PolicyAutoPatch:
		return p, nil
	}
	return "", fmt.Errorf("invalid update.policy %q (want %s)", v, strings.Join(ValidPolicies, " or "))
}

// ShouldAutoApply is the auto-patch decision (D254): only with the opt-in
// policy, only for a patch release, and only through a channel whose own
// script replaces the binary atomically. go-install, container and unknown
// never qualify, whatever the policy.
func ShouldAutoApply(policy string, res Result, ch Channel) bool {
	return policy == PolicyAutoPatch && res.Available && res.Kind == KindPatch && ch.ApplyArgv() != nil
}

// File names inside the cache directory used by the automatic patch.
const (
	applyLockName  = "update-apply.lock"
	appliedMarker  = "update-applied.json"
	failedMarker   = "update-failed.json"
	LogFileName    = "update.log"
	applyLockStale = time.Hour
)

// ErrApplyInProgress is returned by AcquireApplyLock when another starter
// holds the lock: the second one is a no-op.
var ErrApplyInProgress = errors.New("an automatic update is already running")

// AcquireApplyLock takes the lock that makes concurrent session starts start
// one `update apply`, not one each. A lock older than an hour belongs to an
// apply that died and is taken over. The returned func releases it.
func AcquireApplyLock(cacheDir string, now time.Time) (func(), error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(cacheDir, applyLockName)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), now.UTC().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(path)
		if statErr != nil || now.Sub(info.ModTime()) < applyLockStale {
			return nil, ErrApplyInProgress
		}
		os.Remove(path)
	}
	return nil, ErrApplyInProgress
}

// ReleaseApplyLock removes the lock; `update apply` calls it when it ends,
// since the process that took the lock is not the one that finishes the work.
func ReleaseApplyLock(cacheDir string) {
	os.Remove(filepath.Join(cacheDir, applyLockName))
}

// Marker records the outcome of one automatic patch.
type Marker struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Log     string    `json:"log,omitempty"`
}

// WriteMarker records a success (ok) or a failure for version.
func WriteMarker(cacheDir string, ok bool, m Marker) error {
	name := failedMarker
	if ok {
		name = appliedMarker
		os.Remove(filepath.Join(cacheDir, failedMarker))
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, name), data, 0o644)
}

// ReadMarker returns the recorded success or failure, if any.
func ReadMarker(cacheDir string, ok bool) (Marker, bool) {
	name := failedMarker
	if ok {
		name = appliedMarker
	}
	data, err := os.ReadFile(filepath.Join(cacheDir, name))
	if err != nil {
		return Marker{}, false
	}
	var m Marker
	if json.Unmarshal(data, &m) != nil || m.Version == "" {
		return Marker{}, false
	}
	return m, true
}

// ClearMarker removes the success marker once its notice was shown.
func ClearMarker(cacheDir string) {
	os.Remove(filepath.Join(cacheDir, appliedMarker))
}
