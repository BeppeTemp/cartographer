package contract

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func promptFM(t *testing.T, value string) *okf.Frontmatter {
	t.Helper()
	fm, err := okf.ParseFrontmatter("providers:\n  kiro:\n    prompt_file: " + value + "\n")
	if err != nil {
		t.Fatal(err)
	}
	return fm
}

func TestPromptFile_Parser(t *testing.T) {
	long := strings.Repeat("a", 513)
	cases := []struct {
		name, value, errSub string
	}{
		{"relative", ".claude/agents/x.md", ""},
		{"inner dots", "a/../b.md", ""},
		{"absolute", "/etc/passwd", "relative"},
		{"parent", "../x.md", "inside the workspace"},
		{"escaping", "a/../../b", "inside the workspace"},
		{"empty", `""`, "empty"},
		{"drive", `"C:/x.md"`, "relative"},
		{"backslash lead", `"\\x.md"`, "backslash"},
		{"not a string", "12", "string"},
		{"too long", long, "longer"},
	}
	for _, c := range cases {
		fm := promptFM(t, c.value)
		got, err := PromptFileFor(fm, configurator.ProviderKiro)
		verr := ValidateAgentProviders(fm)
		if c.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), c.errSub) || verr == nil {
				t.Errorf("%s: err = %v / validate = %v, want %q", c.name, err, verr, c.errSub)
			}
			continue
		}
		if err != nil || verr != nil || got == "" {
			t.Errorf("%s: got %q, err %v / %v", c.name, got, err, verr)
		}
	}
	// Another client: accepted, remembered for that client only.
	fm, _ := okf.ParseFrontmatter("providers:\n  codex:\n    prompt_file: x.md\n")
	if v, err := PromptFileFor(fm, configurator.ProviderKiro); v != "" || err != nil {
		t.Errorf("kiro read a codex entry: %q %v", v, err)
	}
	if v, err := PromptFileFor(fm, configurator.ProviderCodex); v != "x.md" || err != nil {
		t.Errorf("codex entry: %q %v", v, err)
	}
	if PromptFileCapable(configurator.ProviderCodex) || !PromptFileCapable(configurator.ProviderKiro) {
		t.Error("only kiro renders a prompt_file")
	}
}

// The directive is Cartographer's, never a client's field: no renderer may copy it.
func TestPromptFile_NeverCopiedAsNative(t *testing.T) {
	fm, err := okf.ParseFrontmatter("providers:\n  kiro:\n    prompt_file: x.md\n    tools: [read]\n  codex:\n    prompt_file: y.md\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []configurator.Provider{configurator.ProviderKiro, configurator.ProviderCodex} {
		fields, err := NativeFieldsFor(fm, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fields {
			if f.Key == PromptFileKey {
				t.Errorf("%s: prompt_file leaked into the native fields", p)
			}
		}
	}
}
