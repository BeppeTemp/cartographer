package provisioning

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// The generic descriptor loop resolves Pi's mcp file from its path, so a lock
// entry recorded there is pruned with the right provider.
func TestMCPProviderFromPathResolvesPi(t *testing.T) {
	if got := mcpProviderFromPath(".pi/agent/mcp.json"); got != configurator.ProviderPi {
		t.Errorf("mcpProviderFromPath = %q, want pi", got)
	}
}
