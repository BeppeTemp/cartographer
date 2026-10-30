package system

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// RemovedRevision is the "revision" a removal hashes into its unit ID.
func RemovedRevision(previous string) string { return "removed:" + previous }

// UnitID is "<unit map>/<slug of the key, at most 40 characters>-<8 hex of
// sha256(system Source ID | group key | revision | detection day (UTC))>".
//
// The day is in the hash because a system revisits earlier states (A -> B -> A):
// the second A must create a unit, and a revision-only ID (a feed's) would
// collide with the first. The lookup before the create decides whether the group
// is handled; the day and the create-only batch only guard a same-day race.
func UnitID(unitMap, sourceID, key, revision string, day time.Time) string {
	sum := sha256.Sum256([]byte(sourceID + "|" + key + "|" + revision + "|" + day.UTC().Format("20060102")))
	return unitMap + "/" + slug(key) + "-" + hex.EncodeToString(sum[:4])
}

// slug is the worker's own ASCII slug: lowercase letters and digits, any other
// run becomes one hyphen, none leading or trailing. A key starts with a letter,
// so it is never empty.
func slug(key string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		} else {
			hyphen = true
		}
	}
	s := b.String()
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}
