package contract

import (
	"strings"
	"testing"
)

// The messages are the ones the MCP write channel returned before the
// validators moved here (D191): the plugin reader and the channel share them.
func TestValidateSkillFile(t *testing.T) {
	ok := "---\nname: demo\ndescription: Use when testing.\n---\nBody\n"
	if err := ValidateSkillFile("demo", []byte(ok)); err != nil {
		t.Fatalf("a valid skill is refused: %v", err)
	}
	if err := ValidateSkillFile("other", []byte(ok)); err == nil {
		t.Error("a name that differs from the directory is accepted")
	}
	if err := ValidateSkillFile("demo", []byte("no frontmatter\n")); err == nil {
		t.Error("a skill without frontmatter is accepted")
	}
}

func TestValidateAgentFile_QuotedColonAccepted(t *testing.T) {
	in := "---\nname: helper\ndescription: \"Use when a: b\"\n---\nBody\n"
	if err := ValidateAgentFile("helper", []byte(in)); err != nil {
		t.Fatalf("the quoted form is refused: %v", err)
	}
}

func TestValidateFrontmatterYAML(t *testing.T) {
	for _, c := range []struct {
		name, in string
		bad      bool
	}{
		{"no frontmatter", "text: a: b\n", false},
		{"valid", "---\nname: a\n---\nbody\n", false},
		{"colon in a plain scalar", "---\ndescription: x: y\n---\n", true},
		{"unterminated", "---\nname: a\n", true},
	} {
		if err := ValidateFrontmatterYAML([]byte(c.in)); (err != nil) != c.bad {
			t.Errorf("%s: err = %v; want error %v", c.name, err, c.bad)
		}
	}
}

func TestValidateAgentFile(t *testing.T) {
	ok := "---\nname: helper\ndescription: Helps.\n---\nBody\n"
	if err := ValidateAgentFile("helper", []byte(ok)); err != nil {
		t.Fatalf("a valid agent is refused: %v", err)
	}
	for _, c := range []struct{ slug, in, want string }{
		{"helper", "Body\n", "frontmatter is required"},
		{"helper", "---\ndescription: x\n---\n", "'name' is required"},
		{"other", ok, "must equal the file name"},
		{"helper", "---\nname: helper\n---\n", "'description' is required"},
		// The OKF reader accepts this, yaml.v3 (the approval gate's parser) does not (#1984).
		{"helper", "---\nname: helper\ndescription: Use when a: b\n---\nBody\n", "not valid YAML"},
	} {
		err := ValidateAgentFile(c.slug, []byte(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ValidateAgentFile(%q, %q) = %v; want %q", c.slug, c.in, err, c.want)
		}
	}
}
