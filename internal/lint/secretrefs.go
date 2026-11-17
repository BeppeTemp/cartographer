package lint

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sops"
)

// secretRefFindings reports the secret_refs entries that cannot resolve: the
// file is not in the KB, or (when sops leaves the key names readable) it has no
// such key. Nothing is decrypted. The message names the reference, the
// KB-relative file and the pointer, never a server path (#1278). An entry that
// is not NAME=secrets/file.sops.yaml#/pointer (no =, no #, an empty name, file
// or pointer, an empty entry, a scalar instead of a list) is itself a finding
// that names the expected form (#2223): this check is the write gate's refusal.
// secretRefForm is the form a secret_refs entry takes, as the refusals show it.
const secretRefForm = "NAME=secrets/file.sops.yaml#/json-pointer"

func secretRefFindings(fm *okf.Frontmatter, relPath, kbRoot string) []Finding {
	raw, ok := fm.Get("secret_refs")
	if !ok || kbRoot == "" {
		return nil
	}
	entries, isList := raw.([]string)
	var out []Finding
	if !isList {
		return []Finding{newFinding("secret_ref_broken", Finding{
			Path:    relPath,
			Message: "secret_refs must be a list of " + secretRefForm + ", not a single value",
		})}
	}
	for _, entry := range entries {
		eq, hash := strings.Index(entry, "="), strings.Index(entry, "#")
		if eq <= 0 || hash <= eq+1 || hash == len(entry)-1 {
			out = append(out, newFinding("secret_ref_broken", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("secret_refs entry %q is malformed: expected %s", entry, secretRefForm),
			}))
			continue
		}
		name, file, ptr := entry[:eq], entry[eq+1:hash], entry[hash+1:]
		if strings.HasPrefix(entry[eq+1:], "hook:") {
			continue // served by a resolver hook, not a file of the KB
		}
		// A reference that can never resolve (a path that leaves secrets/, a
		// pointer that is no JSON Pointer) is refused here, at write time,
		// instead of later by secret_resolve.
		if !sops.InSecretsDir(file) {
			out = append(out, newFinding("secret_ref_broken", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("secret_refs %s: the file must be a canonical path under secrets/ (no .. or . segment), as secrets/<name>.sops.yaml; fix the reference or create the secret with secret_set", name),
			}))
			continue
		}
		if !strings.HasPrefix(ptr, "/") {
			out = append(out, newFinding("secret_ref_broken", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("secret_refs %s: the key must be a JSON Pointer starting with /, as secrets/<name>.sops.yaml#/key; fix the reference", name),
			}))
			continue
		}
		if problem := sops.RefProblem(kbRoot, file, ptr); problem != "" {
			out = append(out, newFinding("secret_ref_broken", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("secret_refs %s: %s; fix the reference or create the secret with secret_set", name, problem),
			}))
		}
	}
	return out
}
