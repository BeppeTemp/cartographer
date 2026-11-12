package recipetest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileHoldsTheScenarioToItsShape(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("page.json", `["a"]`)
	write("ok.yaml", "env:\n  ROOT: \"{tests}/x\"\nhttp:\n  - method: GET\n    url: https://wiki.example.com/\n    response:\n      body_file: page.json\nsteps:\n  - verb: list\n")
	sc, err := ReadFile(dir, "ok")
	abs, _ := filepath.Abs(dir)
	if err != nil || sc.HTTP[0].Response.Body != `["a"]` || sc.Env["ROOT"] != abs+"/x" {
		t.Fatalf("%+v %v", sc, err)
	}
	for name, doc := range map[string]string{
		"nolist":  "steps:\n  - verb: check\n",
		"verb":    "steps:\n  - verb: sync\n",
		"unknown": "stepz: []\n",
		"escape":  "http:\n  - method: GET\n    url: https://wiki.example.com/\n    response:\n      body_file: ../x\nsteps:\n  - verb: list\n",
	} {
		write(name+".yaml", doc)
		if _, err := ReadFile(dir, name); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadFileRefusesSymlinksAndBoundsTheTotal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`"x"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.json")); err != nil {
		t.Skip("no symlinks here")
	}
	doc := func(files ...string) string {
		s := "http:\n"
		for _, f := range files {
			s += "  - method: GET\n    url: https://wiki.example.com/\n    response:\n      body_file: " + f + "\n"
		}
		return s + "steps:\n  - verb: list\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "link.yaml"), []byte(doc("link.json")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(dir, "link"); err == nil {
		t.Error("a symlinked body_file was read")
	}
	big := make([]byte, maxFile-10)
	var names []string
	for i := range 5 {
		n := "big" + string(rune('a'+i)) + ".json"
		if err := os.WriteFile(filepath.Join(dir, n), big, 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.yaml"), []byte(doc(names...)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(dir, "many"); err == nil {
		t.Error("five near-cap body_files were read: the total is not bounded")
	}
}
