package mcpserver

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// Atlas searches as the reader types and has nowhere to show the block (D781):
// the response it relays carries none, however much the hits link.
func TestUIAPI_SearchAsYouTypeCarriesNoRelatedBlock(t *testing.T) {
	t.Parallel()
	const query = "gone" // only visible/alpha says it; alpha links beta and secret

	// Control: the same query through the tool does offer a block, so the
	// absence below is the UI's doing and not a fixture that links nothing.
	k := uiFixtureKB(t, "docs")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}})
	text, isErr := callJSON(t, s, ctx, "search", `{"query":"`+query+`","related":5}`)
	if isErr || !strings.Contains(text, `"related"`) {
		t.Fatalf("control: the tool offered no block: %s", text)
	}

	handler := homeUIHandler(t)
	for _, token := range []string{"whole", "narrow"} {
		rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/search?q="+query, token)
		body := decodeUI(t, rr)
		if body["count"] != float64(1) {
			t.Fatalf("%s: fixture: want exactly the one hit, got %s", token, rr.Body.String())
		}
		if _, ok := body["related"]; ok {
			t.Errorf("%s: the Atlas search relayed a related block: %s", token, rr.Body.String())
		}
	}
}
