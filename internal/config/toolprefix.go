package config

import (
	"fmt"
	"regexp"
	"strings"
)

// toolPrefixInvalidChar matches any run of characters outside [a-z0-9_], the
// only characters allowed in a sanitised MCP tool-name prefix.
var toolPrefixInvalidChar = regexp.MustCompile(`[^a-z0-9_]+`)

// toolPrefixRepeatUnderscore collapses runs of underscores left behind by
// toolPrefixInvalidChar substitutions (e.g. "ai - team" → "ai___team").
var toolPrefixRepeatUnderscore = regexp.MustCompile(`_+`)

// SanitizeToolPrefix normalises raw into a value safe to combine with "__"
// and an MCP tool name: lowercase, every run of characters outside
// [a-z0-9_] collapsed to a single "_", repeated "_" collapsed, then
// leading/trailing "_" stripped. Mirrors the character rules MCP clients
// (Kiro) apply to tool names themselves, so "<prefix>__<tool>" stays valid.
//
//	SanitizeToolPrefix("ai-team") == "ai_team"
//	SanitizeToolPrefix("AI Team") == "ai_team"
//	SanitizeToolPrefix("---")     == ""
func SanitizeToolPrefix(raw string) string {
	s := strings.ToLower(raw)
	s = toolPrefixInvalidChar.ReplaceAllString(s, "_")
	s = toolPrefixRepeatUnderscore.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// ValidateToolPrefixShape rejects a sanitised prefix that is empty or starts
// with a digit — either would produce an empty or invalid tool name once
// combined with "__<tool>". It does not check the resulting tool-name
// length budget: that needs the KB's actual registered tool names, checked
// separately at KB-mount time (mcpserver.MultiKBServer.MountKBWithPrefix).
func ValidateToolPrefixShape(sanitized string) error {
	if sanitized == "" {
		return fmt.Errorf("empty after sanitisation")
	}
	if sanitized[0] >= '0' && sanitized[0] <= '9' {
		return fmt.Errorf("starts with a digit after sanitisation: %q", sanitized)
	}
	return nil
}

// ValidateToolPrefixUniqueness reports an error when prefix is already taken by
// another mounted KB. Unprefixed KBs are skipped: an empty prefix is the absence
// of one, and two unprefixed KBs are the case flatNamespaceMountWarning covers
// deliberately as a warning, because a per-server-namespaced client must keep
// working untouched (D102).
//
// The comparison is on the **sanitised** result, because sanitisation is what
// creates the collisions: SanitizeToolPrefix is lossy by design, so "my-kb",
// "my_kb" and "My KB" all derive the same prefix, and two explicit
// kbs[].tool_prefix values were never compared against each other at all. There
// was no uniqueness check anywhere in the resolution path, so a deployment that
// had done everything right could still be silently ambiguous (D152).
func ValidateToolPrefixUniqueness(takenBy map[string]string, kbName, prefix, rawPrefix string) error {
	if prefix == "" {
		return nil
	}
	other, taken := takenBy[prefix]
	if !taken {
		return nil
	}
	detail := ""
	if rawPrefix != prefix {
		detail = fmt.Sprintf(" (derived from %q)", rawPrefix)
	}
	return fmt.Errorf("KB %q and KB %q resolve to the same MCP tool prefix %q%s: their tools would be indistinguishable — set a different kbs[].tool_prefix on one of them",
		other, kbName, prefix, detail)
}

// ResolveToolPrefix determines the tool-name prefix for one KB:
// spec.ToolPrefix (explicit, per-KB) wins over mode; mode == "kb-name"
// derives the prefix from kbName; anything else ("off" or unset) leaves the
// KB unprefixed ("", nil). A non-empty result is always sanitised
// (SanitizeToolPrefix) and shape-validated (ValidateToolPrefixShape) before
// being returned — an error here is a fail-fast config error naming the KB;
// the caller must not mount the KB.
func ResolveToolPrefix(spec KBSpec, mode string, kbName string) (string, error) {
	raw := spec.ToolPrefix
	if raw == "" {
		if mode != "kb-name" {
			return "", nil
		}
		raw = kbName
	}
	sanitized := SanitizeToolPrefix(raw)
	if err := ValidateToolPrefixShape(sanitized); err != nil {
		return "", fmt.Errorf("KB %q: invalid tool_prefix %q: %w", kbName, raw, err)
	}
	return sanitized, nil
}
