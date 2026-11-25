package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// The Surveyor's writes name it in the Cartographer-Surveyor header and never
// in an author: a forge with a membership push rule refuses an author or
// committer that is not a member (D950), and the server must give the commit
// the KB's identity.
func TestSurveyorKBClientCarriesItsIdentityNotAnAuthor(t *testing.T) {
	env := &surveyorEnv{client: client.New("https://meridian.example.com/mcp", "tok")}
	if env.kbClient("kb-a").Surveyor != "" {
		t.Error("a Surveyor identity before the replica id is known")
	}
	env.surveyor = surveyorID("docs-night", "docs-night-0123456789ab")
	for _, routed := range []bool{false, true} {
		env.routed = routed
		c := env.kbClient("kb-a")
		if c.Author != "" {
			t.Errorf("routed=%v: author %q, want none", routed, c.Author)
		}
		if c.Surveyor != "docs-night/docs-night-0123456789ab" || !componentproto.ValidSurveyorID(c.Surveyor) {
			t.Errorf("routed=%v: Surveyor %q", routed, c.Surveyor)
		}
	}
}
