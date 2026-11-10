package provisioning

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// translateAgentForCline adapts an "agent" artifact (a Claude Code subagent .md)
// into a Cline agent file, ~/.cline/agents/<name>.yaml (bundle 4.1.26): a YAML
// frontmatter block with `name` and `description` and a Markdown body that is
// the system prompt.
//
// `tools` and `model` are dropped, as for Kiro: their names are Claude's and not
// portable. `providers.cline` is the way an author grants a tool (`tools`,
// `skills`, `providerId`, `modelId`, `maxIterations`).
//
// Trap: Cline rejects an empty prompt and skips the file silently, so a body
// that is blank after trimming falls back to the description. The extension must
// be .yaml (or .yml): a .md agent is never loaded.
func translateAgentForCline(name string, content []byte) ([]byte, error) {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))
	description := name
	var native []contract.NativeField
	if hasFM {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			return nil, fmt.Errorf("provisioning: parse frontmatter agent %s: %w", name, err)
		}
		if v, ok := fm.Get("description"); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				description = s
			}
		}
		if native, err = contract.NativeFieldsFor(fm, configurator.ProviderCline); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		body = string(content)
	}
	extra, err := renderNativeYAML(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}
	if strings.TrimSpace(body) == "" {
		body = description + "\n"
	}

	var sb strings.Builder
	sb.WriteString("---\nname: ")
	sb.WriteString(contract.YamlQuoteScalar(name))
	sb.WriteString("\ndescription: ")
	sb.WriteString(contract.YamlQuoteScalar(description))
	sb.WriteString("\n")
	sb.WriteString(extra)
	sb.WriteString("---\n")
	sb.WriteString(body)
	return []byte(sb.String()), nil
}
