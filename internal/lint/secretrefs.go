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
// is not NAME=secrets/file.sops.yaml#/pointer is left to the write gate's own
// refusal of the declaration.
func secretRefFindings(fm *okf.Frontmatter, relPath, kbRoot string) []Finding {
	raw, ok := fm.Get("secret_refs")
	if !ok || kbRoot == "" {
		return nil
	}
	entries, _ := raw.([]string)
	var out []Finding
	for _, entry := range entries {
		eq, hash := strings.Index(entry, "="), strings.Index(entry, "#")
		if eq <= 0 || hash <= eq+1 || hash == len(entry)-1 {
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
