package lint

import (
	"strings"
	"testing"
)

const goodVariant = "schema: 1\nname: fork-cli\ndisplay_name: Fork CLI\nbase: crush\ndetect:\n  binaries: [fork-cli]\nmcp:\n  mode: cli\n"

func TestLint_VariantInvalid(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "variants/fork-cli.yaml", goodVariant)
	writeFile(t, k.Root, "variants/future.yaml", strings.Replace(strings.Replace(goodVariant, "fork-cli", "future", 1), "schema: 1", "schema: 2", 1))
	writeFile(t, k.Root, "variants/other.yaml", goodVariant) // name differs from the file stem

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range findings {
		if f.Check == "variant_invalid" {
			got[f.Path] = f.Message
		}
	}
	if len(got) != 2 || got["variants/future.yaml"] == "" || got["variants/other.yaml"] == "" {
		t.Fatalf("want findings for future.yaml and other.yaml only, got %v", got)
	}
}

func TestLint_VariantInvalidNotInScopedRun(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "variants/bad.yaml", "schema: 2\n")
	findings, err := Run(k, "some/scope", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "variant_invalid" {
			t.Errorf("a scoped lint must not report KB-level variant findings: %v", f)
		}
	}
}
