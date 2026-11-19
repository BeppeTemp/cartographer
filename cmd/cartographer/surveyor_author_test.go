package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// The Surveyor's writes name their author in the form the Atlas reads as a
// Surveyor's, and one the server accepts.
func TestSurveyorKBClientCarriesItsAuthor(t *testing.T) {
	env := &surveyorEnv{client: client.New("https://meridian.example.com/mcp", "tok")}
	if env.kbClient("kb-a").Author != "" {
		t.Error("an author before the replica id is known")
	}
	env.author = surveyorAuthor("docs-night", "docs-night-0123456789ab")
	for _, routed := range []bool{false, true} {
		env.routed = routed
		c := env.kbClient("kb-a")
		wire, _ := client.DecodeAuthor(client.EncodeAuthor(c.Author))
		name, email, problem := gitx.ParseIdent(wire)
		if problem != "" || name != "surveyor/docs-night/docs-night-0123456789ab" || !strings.HasSuffix(email, "@surveyor.invalid") {
			t.Errorf("routed=%v: author %q parses to %q %q (%s)", routed, c.Author, name, email, problem)
		}
	}
}
