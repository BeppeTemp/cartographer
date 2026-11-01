// Package poll names units.
//
// It holds what the task decides without a model: the instructions of its one
// model call, the identity of a feed unit and the check of the model's answer.
//
// The identity is the idempotency token of the poll (D1262): a unit that exists
// means its entry was handled, so a crash anywhere re-lists the same entries
// and converges, and a model call is never repeated for an entry that has its
// unit.
package poll

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Instructions is the system prompt of the poll's model call, embedded in the
// binary: instructions the worker follows never come from a KB.
//
//go:embed instructions.md
var Instructions string

// Limits of an Answer.
const (
	MaxTargets  = 20
	MaxLine     = 500
	MaxBodyByte = 8 << 10
)

// Executors is the vocabulary of an IngestUnit's executor (a test pins it to
// kb.IngestExecutors; this package may not import internal/kb at run time).
var Executors = []string{"any", "large", "human"}

var (
	slugRE = regexp.MustCompile(`[^a-z0-9]+`)
	idRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)+$`)
)

// UnitID is "<unit map>/<slug of the entry key, at most 40 characters>-<8 hex of
// sha256(feed Source ID | entry locator | revision)>": two segments (assets need
// them), derived from nothing that can change without the revision, never from
// the title.
func UnitID(unitMap, feedID, locator, revision, key string) string {
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(key), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "entry"
	}
	sum := sha256.Sum256([]byte(feedID + "|" + locator + "|" + revision))
	return unitMap + "/" + slug + "-" + hex.EncodeToString(sum[:4])
}

// Answer is the model's answer: the fields of the unit it shapes.
type Answer struct {
	Targets  []string `json:"targets"`
	Extract  string   `json:"extract"`
	Verify   string   `json:"verify"`
	Executor string   `json:"executor"`
	Body     string   `json:"body"`
}

// ParseAnswer extracts the JSON object of text and validates it. maps are the
// plan's maps: when there are any, every target is inside one of them. The
// error text goes back to the model once, so it names the problem.
func ParseAnswer(text string, maps []string) (Answer, error) {
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j < i {
		return Answer{}, errors.New("the answer is not a JSON object")
	}
	var a Answer
	if err := json.Unmarshal([]byte(text[i:j+1]), &a); err != nil {
		return Answer{}, fmt.Errorf("the answer is not valid JSON: %v", err)
	}
	if !slices.Contains(Executors, a.Executor) {
		return Answer{}, fmt.Errorf("executor %q must be one of %s", a.Executor, strings.Join(Executors, ", "))
	}
	if len(a.Targets) < 1 || len(a.Targets) > MaxTargets {
		return Answer{}, fmt.Errorf("targets must hold 1 to %d concept IDs", MaxTargets)
	}
	for _, t := range a.Targets {
		if !idRE.MatchString(t) || strings.Contains(t, "..") || strings.HasSuffix(t, ".md") {
			return Answer{}, fmt.Errorf("target %q is not a concept ID such as map/concept", t)
		}
		if len(maps) > 0 && !slices.Contains(maps, strings.SplitN(t, "/", 2)[0]) {
			return Answer{}, fmt.Errorf("target %q is outside the plan's maps (%s)", t, strings.Join(maps, ", "))
		}
	}
	for name, v := range map[string]string{"extract": a.Extract, "verify": a.Verify} {
		if v = strings.TrimSpace(v); v == "" || strings.ContainsAny(v, "\r\n") || utf8.RuneCountInString(v) > MaxLine {
			return Answer{}, fmt.Errorf("%s must be one line of at most %d characters", name, MaxLine)
		}
	}
	if strings.TrimSpace(a.Body) == "" || len(a.Body) > MaxBodyByte {
		return Answer{}, fmt.Errorf("body must be non-empty and at most %d bytes", MaxBodyByte)
	}
	return a, nil
}
