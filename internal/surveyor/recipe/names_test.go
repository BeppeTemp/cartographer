package recipe

import (
	"context"
	"strings"
	"testing"
)

func TestNameGrammar(t *testing.T) {
	for name, tc := range map[string]struct {
		in            string
		local, qual   bool
		plugin, short string
	}{
		"local":           {"my-feed", true, false, "", ""},
		"qualified":       {"acme-wiki/wiki", false, true, "acme-wiki", "wiki"},
		"digits local":    {"7-docs", true, false, "", ""},
		"plugin digit":    {"7acme/wiki", false, false, "", ""},
		"upper":           {"Acme/wiki", false, false, "", ""},
		"two slashes":     {"a-b/c/d", false, false, "", ""},
		"empty local":     {"acme/", false, false, "", ""},
		"empty plugin":    {"/wiki", false, false, "", ""},
		"one-char plugin": {"a/wiki", false, false, "", ""},
		"alias form":      {"acme--wiki", true, false, "", ""},
		"long local":      {strings.Repeat("a", 41), false, false, "", ""},
		"long qualified":  {"acme/" + strings.Repeat("a", 41), false, false, "", ""},
		"underscore":      {"acme/wi_ki", false, false, "", ""},
		"plugin ends -":   {"a-/b", false, false, "", ""},
		"local starts -":  {"ab/-b", false, false, "", ""},
		"local ends -":    {"ab/b-", false, false, "", ""},
		"one-char local":  {"ab/c", false, true, "ab", "c"},
		"inner dashes":    {"a-b/c-d", false, true, "a-b", "c-d"},
		"at the cap":      {strings.Repeat("a", 15) + "/" + strings.Repeat("b", 15), false, true, strings.Repeat("a", 15), strings.Repeat("b", 15)},
		"over the cap":    {strings.Repeat("a", 16) + "/" + strings.Repeat("b", 15), false, false, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if ValidLocal(tc.in) != tc.local || ValidQualified(tc.in) != tc.qual {
				t.Fatalf("local %v qualified %v", ValidLocal(tc.in), ValidQualified(tc.in))
			}
			p, l, ok := Split(tc.in)
			if ok != tc.qual || p != tc.plugin || l != tc.short {
				t.Fatalf("Split = %q %q %v", p, l, ok)
			}
			if tc.qual && Qualified(p, l) != tc.in {
				t.Fatalf("Qualified = %q", Qualified(p, l))
			}
		})
	}
}

type namedRecipe struct{ name string }

func (n namedRecipe) Name() string         { return n.name }
func (n namedRecipe) Shape() string        { return ShapeFeed }
func (n namedRecipe) Instructions() string { return "text" }
func (n namedRecipe) List(context.Context, Request) (Listing, error) {
	return Listing{}, nil
}

func TestRegistryHoldsPluginRecipesUnderTheirQualifiedName(t *testing.T) {
	r, err := NewRegistry(Options{Plugin: []Recipe{namedRecipe{"acme-wiki/wiki"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get("acme-wiki/wiki"); !ok || got.Name() != "acme-wiki/wiki" {
		t.Fatalf("Get = %v %v", got, ok)
	}
	if _, ok := r.Get("wiki"); ok {
		t.Fatal("the local name alone resolves to a plugin recipe")
	}
	for name, o := range map[string]Options{
		"unprefixed plugin recipe": {Plugin: []Recipe{namedRecipe{"wiki"}}},
		"clash with a built-in":    {Plugin: []Recipe{namedRecipe{"git-docs"}}},
		"twice":                    {Plugin: []Recipe{namedRecipe{"acme/wiki"}, namedRecipe{"acme/wiki"}}},
		"clash with an exec": {Exec: []ExecConfig{{Name: "acme/wiki"}},
			Plugin: []Recipe{namedRecipe{"acme/wiki"}}},
	} {
		if _, err := NewRegistry(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
