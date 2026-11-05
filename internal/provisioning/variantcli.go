package provisioning

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
)

// kbDeclaresCLIVariant reports whether a KB root declares a client variant in
// mcp.mode cli. The server does not know which client asks for a manifest, so
// the bundled cartographer-cli skill is listed whenever a served KB declares
// such a variant; the client filter (cmd/cartographer withoutCLISkill) keeps it
// from every provider that does not reach Cartographer through the command.
// Only a descriptor that parses counts, like the manifest's own variant scan;
// whether it is tracked or approved is not asked here (the filter is the second
// line), so a KB with a stale cli descriptor costs one listed skill, no more.
func kbDeclaresCLIVariant(kbRoots map[string]string) bool {
	for _, root := range kbRoots {
		dir := filepath.Join(root, "variants")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, over, err := ReadFileCapped(filepath.Join(dir, e.Name()))
			if err != nil || over != 0 {
				continue
			}
			if spec, err := clientvariant.Parse(strings.TrimSuffix(e.Name(), ".yaml"), data); err == nil && spec.MCP.Mode == "cli" {
				return true
			}
		}
	}
	return false
}
