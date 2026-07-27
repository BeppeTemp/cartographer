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
