package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// `requires` is the list a skill, an agent or a hook declares of what it needs
// on a client (D1656): `<kind>:<name>` for another artifact (skill, agent or
// hook), or a capability id of the capability table. Carriers: the `requires`
// frontmatter key of SKILL.md and of agents/<name>.md (a list), the `requires`
// array of hook.json. A hook has no frontmatter and ParseHookSpec is lenient,
// so a typo there is ignored on the client: ValidateRequires, run by the write
// gate and by lint, is the only guard.

// ErrHookJSON marks a hook.json that is not valid JSON: ValidateHookJSON
// owns that finding, so lint does not report it a second time as requires.
var ErrHookJSON = errors.New("invalid JSON")

// MaxRequires caps one `requires` list.
const MaxRequires = 32

// RequiresKey is the key of the frontmatter and of hook.json.
const RequiresKey = "requires"

// requirableKinds are the artifact kinds a `requires` entry may name.
var requirableKinds = map[string]bool{"skill": true, "agent": true, "hook": true}

// ValidateRequires checks one `requires` list of the artifact kind/name (name
// "" skips the self-reference check). It rejects, naming the entry: an empty
// or duplicate entry, a negated (`!`) form, an unknown kind, an `mcp:` or
// `instructions:` reference, an invalid artifact name, a self-reference, an
// unknown capability id, and a list over MaxRequires. A capability id is
// checked against the id set, never its status: the status depends on the
// client. A reference to an artifact that exists nowhere is not an error: it
// may come from another KB bound to the same client.
func ValidateRequires(kind, name string, entries []string) error {
	if len(entries) > MaxRequires {
		return fmt.Errorf("requires has %d entries, at most %d are allowed", len(entries), MaxRequires)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("requires has an empty entry")
		}
		if seen[e] {
			return fmt.Errorf("requires entry %q is duplicated", e)
		}
		seen[e] = true
		if strings.HasPrefix(e, "!") {
			return fmt.Errorf("requires entry %q: a negated requirement is not supported", e)
		}
		k, n, isRef := strings.Cut(e, ":")
		if !isRef {
			if !IsCapability(e) {
				return fmt.Errorf("requires entry %q is neither <kind>:<name> nor a known capability id", e)
			}
			continue
		}
		switch {
		case k == "mcp" || k == "instructions":
			return fmt.Errorf("requires entry %q: %s cannot be required, only skill, agent or hook", e, k)
		case !requirableKinds[k]:
			return fmt.Errorf("requires entry %q: unknown kind %q (skill, agent or hook)", e, k)
		case !ValidArtifactName(n):
			return fmt.Errorf("requires entry %q: %q is not a valid artifact name", e, n)
		case name != "" && k == kind && n == name:
			return fmt.Errorf("requires entry %q refers to the artifact itself", e)
		}
	}
	return nil
}

// RequiresFromFrontmatter reads the `requires` key of a skill's or agent's
// frontmatter: (nil, false, nil) when absent, an error when it is not a list of
// strings (a scalar or a nested block). The list forms are the ones the OKF
// reader yields for `tools`: a flow list or a block list.
func RequiresFromFrontmatter(fm *okf.Frontmatter) ([]string, bool, error) {
	v, ok := fm.Get(RequiresKey)
	if !ok {
		return nil, false, nil
	}
	list, isList := v.([]string)
	if !isList {
		return nil, true, fmt.Errorf("requires must be a list of strings, e.g. [skill:name, agent:name]")
	}
	return list, true, nil
}

// ValidateFrontmatterRequires is the check of a skill's or agent's frontmatter.
func ValidateFrontmatterRequires(kind, name string, fm *okf.Frontmatter) error {
	list, _, err := RequiresFromFrontmatter(fm)
	if err != nil {
		return err
	}
	return ValidateRequires(kind, name, list)
}

// hookRequires reads the `requires` array of hook.json. HookSpec does not
// carry it: a wrong type there must not stop the hook from registering.
func hookRequires(data []byte) ([]string, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrHookJSON, err)
	}
	r, ok := raw[RequiresKey]
	if !ok {
		return nil, false, nil
	}
	// null is refused like a bare `requires:` in frontmatter: both carriers
	// treat an empty value as a mistake, and an empty list is spelled [].
	var list []string
	if string(r) == "null" || json.Unmarshal(r, &list) != nil {
		return nil, true, fmt.Errorf("requires must be an array of strings, e.g. [\"skill:name\"]")
	}
	return list, true, nil
}

// ValidateHookRequires checks the `requires` array of a hook.json (name "" skips
// the self-reference check). Not part of ValidateHookJSON: a bad `requires`
// never stops a hook from registering, so the write gate and lint call it on
// its own and word it as such.
func ValidateHookRequires(name string, data []byte) error {
	list, _, err := hookRequires(data)
	if err != nil {
		return err
	}
	return ValidateRequires("hook", name, list)
}

// RequiresOf reads the requirements of an artifact from its source files,
// the pattern of ArtifactRequiresApproval: nothing travels on the wire and the
// Artifact JSON carries nothing. It returns an error when the source cannot be
// read or its `requires` is invalid; a sync treats that as "no requirements"
// plus a warning, never a failure (same tolerance as ParseHookSpec). Other
// kinds, and the bundle's skills without the key, have none.
func RequiresOf(a Artifact, bundleFS fs.FS, kbRoots map[string]string) ([]string, error) {
	if a.Kind != "skill" && a.Kind != "agent" && a.Kind != "hook" {
		return nil, nil
	}
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, bundleFS, kbRoots)
		if err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		p := path.Clean(filepath.ToSlash(f.Path))
		var list []string
		switch {
		case a.Kind == "hook" && p == "hook.json":
			l, _, err := hookRequires(f.Content)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", a.Kind, a.Name, err)
			}
			list = l
		case (a.Kind == "skill" && p == "SKILL.md") || (a.Kind == "agent" && strings.HasSuffix(p, ".md")):
			fmRaw, _, hasFM := okf.SplitFrontmatter(string(f.Content))
			if !hasFM {
				continue
			}
			fm, err := okf.ParseFrontmatter(fmRaw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: parse frontmatter: %w", a.Kind, a.Name, err)
			}
			l, _, err := RequiresFromFrontmatter(fm)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", a.Kind, a.Name, err)
			}
			list = l
		default:
			continue
		}
		if err := ValidateRequires(a.Kind, a.Name, list); err != nil {
			return nil, fmt.Errorf("%s %s: %w", a.Kind, a.Name, err)
		}
		return list, nil
	}
	return nil, nil
}
