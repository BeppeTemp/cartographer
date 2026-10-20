package lint

import "testing"

func TestSlugify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "sharp s lower", input: "straße", want: "strasse"},
		{name: "sharp s upper", input: "STRAẞE", want: "strasse"},
		{name: "ae ligature lower", input: "æther", want: "aether"},
		{name: "ae ligature upper", input: "Æther", want: "aether"},
		{name: "oe ligature lower", input: "cœur", want: "coeur"},
		{name: "oe ligature upper", input: "Œuvre", want: "oeuvre"},
		{name: "thorn lower", input: "þing", want: "thing"},
		{name: "thorn upper", input: "Þing", want: "thing"},
		{name: "ij ligature lower", input: "ĳssel", want: "ijssel"},
		{name: "ij ligature upper", input: "Ĳssel", want: "ijssel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Slugify(tt.input)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !slugPattern.MatchString(got) {
				t.Errorf("Slugify(%q) = %q, which does not match slugPattern", tt.input, got)
			}
		})
	}
}
