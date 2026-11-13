package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func rulesFile(t *testing.T, content string) (base, path string) {
	t.Helper()
	base = t.TempDir()
	path = filepath.Join(base, AntigravityLocalInstructions)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return base, path
}

func managedBlock(body string) string {
	return instructionsBlockBegin + "\n" + body + "\n" + instructionsBlockEnd + "\n"
}

// An existing frontmatter that already carries `trigger: always_on` is the
// header: the block is appended below it and the file is in sync afterwards.
func TestAntigravityRules_ExistingAlwaysOnFrontmatterIsKept(t *testing.T) {
	user := "---\ntrigger: always_on\ndescription: mine\n---\n\nmy rule\n"
	base, path := rulesFile(t, user)
	changed, err := writeInstructionsBlockWithHeaderChanged(base, path, "body", antigravityRulesFrontmatter)
	if err != nil || !changed {
		t.Fatalf("write = (%v, %v), want changed", changed, err)
	}
	got, _ := os.ReadFile(path)
	if want := user + "\n" + managedBlock("body"); string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if !blockBodyMatchesHeader(path, "body", antigravityRulesFrontmatter) {
		t.Fatal("a file with the always_on frontmatter and the block must be in sync")
	}
	if changed, err := writeInstructionsBlockWithHeaderChanged(base, path, "body", antigravityRulesFrontmatter); err != nil || changed {
		t.Fatalf("second write = (%v, %v), want no change", changed, err)
	}
}

// Another trigger is the user's own frontmatter: refused, never rewritten, and
// reported as drift.
func TestAntigravityRules_ForeignFrontmatterIsRefusedUntouched(t *testing.T) {
	for name, user := range map[string]string{
		"other trigger": "---\ntrigger: manual\n---\n\nmine\n",
		"no trigger":    "---\ndescription: mine\n---\n\nmine\n",
		"unclosed":      "---\ntrigger: always_on\nmine\n",
	} {
		t.Run(name, func(t *testing.T) {
			base, path := rulesFile(t, user+managedBlock("body"))
			before, _ := os.ReadFile(path)
			changed, err := writeInstructionsBlockWithHeaderChanged(base, path, "body", antigravityRulesFrontmatter)
			if changed || !errors.Is(err, ErrForeignFrontmatter) || !IsRefusal(err) {
				t.Fatalf("write = (%v, %v), want a refusal", changed, err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatalf("file rewritten: %q", after)
			}
			if blockBodyMatchesHeader(path, "body", antigravityRulesFrontmatter) {
				t.Fatal("a file Antigravity would not load must not be reported in sync")
			}
		})
	}
}

// No frontmatter at all: the header is added above the user's text.
func TestAntigravityRules_NoFrontmatterGetsTheHeader(t *testing.T) {
	base, path := rulesFile(t, "plain rule\n")
	if blockBodyMatchesHeader(path, "body", antigravityRulesFrontmatter) {
		t.Fatal("a file without frontmatter is not in sync")
	}
	if changed, err := writeInstructionsBlockWithHeaderChanged(base, path, "body", antigravityRulesFrontmatter); err != nil || !changed {
		t.Fatalf("write = (%v, %v)", changed, err)
	}
	got, _ := os.ReadFile(path)
	if want := antigravityRulesFrontmatter + "plain rule\n\n" + managedBlock("body"); string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if !blockBodyMatchesHeader(path, "body", antigravityRulesFrontmatter) {
		t.Fatal("in sync after the write")
	}
}
