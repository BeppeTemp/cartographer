package clientconfig_test

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A key Save drops because a release removed it takes with it the comment the
// schema wrote above it in the wording that release had, which the table no longer
// holds: only Retired remembers the text. A note a person wrote is not the schema's.
func TestSaveDropsTheGeneratedCommentOfARetiredKey(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, `server_url: http://127.0.0.1:39273/mcp
# The agent clients connected to the server, for example claude or opencode.
# connect and disconnect maintain it.
# Default: [].
agents:
  - claude
# Name of the environment variable that holds the bearer token.
# Default: CARTOGRAPHER_TOKENS.
token_env: CARTOGRAPHER_TOKENS
# keep me
search_depth: 6
auth: true
`)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, gone := range []string{"The agent clients connected", "Name of the environment variable", "agents:", "token_env", "search_depth"} {
		if strings.Contains(raw, gone) {
			t.Errorf("%q survives the save:\n%s", gone, raw)
		}
	}
	if !strings.Contains(raw, "# keep me") {
		t.Errorf("a person's note was lost:\n%s", raw)
	}
}
