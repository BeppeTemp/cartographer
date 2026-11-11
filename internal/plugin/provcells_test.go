package plugin

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// The resolved fixtures register their cells with provisioning: the cells keep
// their scope and kind, the hook directory joins the base's dispatcher, and an
// MCP file that is a pattern is no destination cell.
func TestResolvedProviderCellsRegisterWithProvisioning(t *testing.T) {
	var cells []provisioning.PluginCells
	for _, tc := range []struct{ name, src string }{{"fork-cli", forkCLI}, {"fork-ext", forkExt}} {
		r, _ := ResolveProvider(parsed(t, tc.name, tc.src), fakeEnv("linux", nil))
		c := ProviderCells(r, true)
		if c.Name != tc.name || c.Base != r.Base || !c.Active {
			t.Fatalf("%s: %+v", tc.name, c)
		}
		if tc.name == "fork-ext" && c.MCPFile != nil {
			t.Errorf("a pattern became a cell: %v", c.MCPFile)
		}
		cells = append(cells, c)
	}
	_, refused := provisioning.ReplacePluginCells(cells, "/home/user")
	t.Cleanup(func() { provisioning.ReplacePluginCells(nil, "/home/user") })
	if len(refused) != 0 {
		t.Fatalf("refused: %+v", refused)
	}
	if cells[1].Global.Hook == nil || cells[1].Global.Instructions == nil || cells[0].Global.Skill == nil {
		t.Errorf("a cell was lost in the conversion: %+v", cells)
	}
	if r, _ := ResolveProvider(parsed(t, "fork-ext", forkExt), fakeEnv("linux", nil)); ProviderCells(r, false).Active {
		t.Error("Active is the plugin's, not the descriptor's")
	}
}
