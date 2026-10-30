package system

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// RemovedRevision is the "revision" a removal hashes into its unit ID.
func RemovedRevision(previous string) string { return "removed:" + previous }

// UnitID is "<unit map>/<slug of the key, at most 40 characters>-<8 hex of
// sha256(system Source ID | group key | revision | seq)>", where seq is how many
// units the group already has, whatever their status.
//
// The count is in the hash because a system revisits earlier states (A -> B -> A,
// even within one day): the second A must create a unit, and an ID from the
// revision (a feed's) or from the revision and the day would collide with the
// first A's, which the create-only write refuses. Every observation of a group
// sees a different count, so its ID is new. The lookup before the create decides
// whether the group is handled (a crash re-run or a rebuilt state finds the unit
// and creates nothing); two pollers that see the same count make the same ID and
// the create-only batch lets one win.
func UnitID(unitMap, sourceID, key, revision string, seq int) string {
	sum := sha256.Sum256([]byte(sourceID + "|" + key + "|" + revision + "|" + strconv.Itoa(seq)))
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
