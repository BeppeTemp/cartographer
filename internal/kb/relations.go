package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
	"gopkg.in/yaml.v3"
)

// Relation is one KB a KB depends on, as its settings.yaml `depends_on` says it:
// Name is the mount name of the target on the server, Remote the target's git
// remote in the normalized host/owner/name form, the guard that tells the KB
// meant from another one mounted under the same name. The JSON tags are the wire
// shape of the relation view (the same type, in the contract, is a copy).
type Relation struct {
	Name   string `json:"name"`
	Remote string `json:"remote"`
}

// relationsKey is the settings.yaml key that carries the relations.
const relationsKey = "depends_on"

// mountNameRe is the rule for a KB mount name. config.ValidateKBName is the
// original; this package cannot import config, and a config test pins the two
// together (TestValidMountNameMatchesConfig).
var mountNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidMountName reports whether s can be a KB mount name.
func ValidMountName(s string) bool { return mountNameRe.MatchString(s) }

// NormalizeDeclaredRemote turns a remote as a KB file declares it into the
// normalized key repoindex compares by (host/owner/name): either that canonical
// form or any clone URL. paths.yaml and depends_on share it, so one repository
// has one key in the whole codebase.
func NormalizeDeclaredRemote(raw string) (string, error) {
	declared := raw
	// The canonical form the registry documents — host/owner/name — has
	// neither a scheme nor the scp-like colon NormalizeRemote needs.
	if !strings.Contains(raw, "://") && !strings.Contains(raw, ":") {
		raw = "https://" + raw
	}
	norm, err := repoindex.NormalizeRemote(raw)
	if err != nil || strings.Count(string(norm), "/") < 2 {
		// The declared text can carry credentials and reaches a settings finding.
		return "", fmt.Errorf("remote %q is not a git remote (expected host/owner/name or a clone URL)", gitx.RedactRemoteURL(declared))
	}
	return string(norm), nil
}

// parseRelations checks the value of `depends_on` item by item, so one typo
// does not drop every dependency: the well-formed items are returned, each bad
// one is a SettingsMalformed keyed depends_on[<i>]. ok is false when the value
// is not a list at all (the key is then left out whole, with one finding).
func parseRelations(v *yaml.Node) (rels []Relation, bad []SettingsMalformed, ok bool) {
	if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		return nil, nil, true // `depends_on:` with no value declares none
	}
	if v.Kind != yaml.SequenceNode {
		return nil, []SettingsMalformed{{Key: relationsKey, Reason: "must be a list of {name, remote} items"}}, false
	}
	seen := map[string]bool{}
	for i, item := range v.Content {
		key := fmt.Sprintf("%s[%d]", relationsKey, i)
		r, reason := parseRelationItem(item)
		if reason == "" && seen[r.Name] {
			reason = fmt.Sprintf("%q declared twice", r.Name)
		}
		if reason != "" {
			bad = append(bad, SettingsMalformed{Key: key, Reason: reason + "; the item is ignored"})
			continue
		}
		seen[r.Name] = true
		rels = append(rels, r)
	}
	return rels, bad, true
}

func parseRelationItem(item *yaml.Node) (Relation, string) {
	if item.Kind != yaml.MappingNode {
		return Relation{}, "must be a mapping with name and remote"
	}
	var name, remote string
	var hasName, hasRemote bool
	for i := 0; i+1 < len(item.Content); i += 2 {
		f, val := item.Content[i].Value, item.Content[i+1]
		if f != "name" && f != "remote" {
			return Relation{}, fmt.Sprintf("unknown field %q", f)
		}
		if val.Kind != yaml.ScalarNode || val.Tag != "!!str" {
			return Relation{}, fmt.Sprintf("field %q must be a string", f)
		}
		switch {
		case f == "name" && !hasName:
			name, hasName = strings.TrimSpace(val.Value), true
		case f == "remote" && !hasRemote:
			remote, hasRemote = strings.TrimSpace(val.Value), true
		default:
			return Relation{}, fmt.Sprintf("field %q declared twice", f)
		}
	}
	if !hasName || !hasRemote {
		return Relation{}, "needs both name and remote"
	}
	if !ValidMountName(name) {
		return Relation{}, fmt.Sprintf("name %q is not a KB name (letters, digits, '-' and '_')", name)
	}
	norm, err := NormalizeDeclaredRemote(remote)
	if err != nil {
		return Relation{}, err.Error()
	}
	return Relation{Name: name, Remote: norm}, ""
}

// Relations returns the relations the working copy's settings.yaml declares
// (well-formed items only). It is for display (SettingsInfo); consumers read
// KB.Relations, which looks at the default branch only.
func (s Settings) Relations() []Relation { return append([]Relation(nil), s.relations...) }

// RelationsAt reads `depends_on` as committed on ref. Like `branch` (D903), the
// working copy is not read, so a working branch cannot widen a binding. An
// absent file or key is none; a git failure is an error, never "none"; a file
// that does not parse, or a malformed item, yields the well-formed items (the
// finding is reported by settings_findings).
func (k *KB) RelationsAt(ref string) ([]Relation, error) {
	relationsGitReads.Add(1)
	text, exists, err := gitx.ShowFileIfExists(k.Root, ref, SettingsFile)
	if err != nil {
		// The raw git output can carry absolute server paths: it goes to the log only.
		fmt.Fprintf(os.Stderr, "cartographer: read %s on %s: %v\n", SettingsFile, ref, err)
		return nil, fmt.Errorf("could not read %s on %s: git did not answer (details in the server log)", SettingsFile, ref)
	}
	if !exists {
		return nil, nil
	}
	s, _, perr := ParseSettings([]byte(text))
	if perr != nil {
		return nil, nil
	}
	return s.Relations(), nil
}

// relationsMaxAge is how long Relations and NormalizedOrigin answer from memory
// without a git call: a change of depends_on on the default branch, or of the
// origin, is seen within it. A variable so tests can set it.
var relationsMaxAge = 3 * time.Second

// relationsGitReads counts the git reads of Relations, RelationsAt and
// NormalizedOrigin (tests only: it is how they assert the memo holds).
var relationsGitReads atomic.Int64

// relationsMemo is the memory of Relations and NormalizedOrigin of one KB.
type relationsMemo struct {
	mu sync.Mutex
	// relations
	ref       string // ref the memo was read on
	sha       string // its commit
	rels      []Relation
	have      bool
	checkedAt time.Time // last time ref/sha were confirmed
	// origin
	origin     string
	originOK   bool
	originHave bool
	originAt   time.Time
}

// forgetRelationsValidation makes the next Relations and NormalizedOrigin look
// at git again: every write goes through WithGitLock, which calls it when the
// write ends.
func (k *KB) forgetRelationsValidation() {
	k.relMemo.mu.Lock()
	k.relMemo.checkedAt = time.Time{}
	k.relMemo.originAt = time.Time{}
	k.relMemo.mu.Unlock()
}

// relationsRef is the ref Relations reads: the remote's default branch when the
// KB has a remote, HEAD only for a KB with no remote (it has no other branch to
// widen from). The default branch is the clone's refs/remotes/<remote>/HEAD, not
// RemoteDefaultBranch: that one is the branch writes go to, which `branch` in
// settings.yaml or kbs[].branch can set to a working branch. A KB with a remote
// whose default branch is not known yet (before the first sync) is an error,
// never HEAD, which can be a working branch.
func (k *KB) relationsRef() (string, error) {
	relationsGitReads.Add(1)
	const remote = "origin"
	if _, exists, err := gitx.RemoteURLIfConfigured(k.Root, remote); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: read the origin of %s: %v\n", filepath.Base(k.Root), err)
		return "", fmt.Errorf("could not read %s: git did not answer (details in the server log)", SettingsFile)
	} else if !exists {
		return "HEAD", nil
	}
	def, ok := gitx.RemoteHeadBranch(k.Root, remote)
	if !ok {
		return "", fmt.Errorf("could not read %s: the default branch of the remote is not known yet (it is learned at the first sync)", SettingsFile)
	}
	// Fully qualified: a tag or a local branch named origin/<def> wins over
	// refs/remotes/ in a short name, and would be read in its place.
	return "refs/remotes/" + remote + "/" + def, nil
}

// Relations returns the KB's relations as committed on the remote's default
// branch (HEAD without a remote), the only reader consumers use. It is memoized
// on the ref's commit: within relationsMaxAge it is an in-memory read, after
// it one rev-parse confirms the commit did not move. An error is never kept.
func (k *KB) Relations() ([]Relation, error) {
	m := &k.relMemo
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.have && now.Sub(m.checkedAt) < relationsMaxAge {
		return append([]Relation(nil), m.rels...), nil
	}
	ref, err := k.relationsRef()
	if err != nil {
		m.have = false
		return nil, err
	}
	relationsGitReads.Add(1)
	sha, err := gitx.HeadSHAAt(k.Root, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: resolve %s: %v\n", ref, err)
		m.have = false
		return nil, fmt.Errorf("could not read %s on %s: git did not answer (details in the server log)", SettingsFile, ref)
	}
	if m.have && m.ref == ref && m.sha == sha {
		m.checkedAt = now
		return append([]Relation(nil), m.rels...), nil
	}
	rels, err := k.RelationsAt(sha)
	if err != nil {
		m.have = false
		return nil, err
	}
	m.have, m.rels, m.ref, m.sha, m.checkedAt = true, rels, ref, sha, now
	return append([]Relation(nil), rels...), nil
}

// NormalizedOrigin is NormalizedOriginErr for a display: a failure to read the
// origin reads as none. A decision (a relation's guard) uses NormalizedOriginErr,
// where a failure is an error and never "no remote".
func (k *KB) NormalizedOrigin() (string, bool) {
	o, ok, err := k.NormalizedOriginErr()
	if err != nil {
		return "", false
	}
	return o, ok
}

// NormalizedOriginErr is the KB's origin in the normalized host/owner/name form
// (the origin URL with credentials redacted, then repoindex.NormalizeRemote),
// and whether it has one. Memoized for relationsMaxAge, because reading it is a
// git call. A KB with no remote, or one whose URL cannot be normalized, has none
// (ok false, nil error); a git failure reading the config is an error, and it is
// never memoized, so it cannot pass for "no remote".
func (k *KB) NormalizedOriginErr() (origin string, ok bool, err error) {
	m := &k.relMemo
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.originHave && now.Sub(m.originAt) < relationsMaxAge {
		return m.origin, m.originOK, nil
	}
	relationsGitReads.Add(1)
	raw, exists, err := gitx.RemoteURLIfConfigured(k.Root, "origin")
	if err != nil {
		m.originHave = false
		fmt.Fprintf(os.Stderr, "cartographer: read the origin of %s: %v\n", filepath.Base(k.Root), err)
		return "", false, fmt.Errorf("could not read the origin of the KB: git did not answer (details in the server log)")
	}
	m.origin, m.originOK = "", false
	if exists {
		if norm, nerr := repoindex.NormalizeRemote(gitx.RedactRemoteURL(raw)); nerr == nil {
			m.origin, m.originOK = string(norm), true
		}
	}
	m.originHave, m.originAt = true, now
	return m.origin, m.originOK, nil
}
