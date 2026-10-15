package mcpserver

import "github.com/BeppeTemp/cartographer/internal/kb"

// setPolicy edits one KB policy field in a test: read the snapshot, change it,
// store it whole (D775).
func setPolicy(k *kb.KB, edit func(p *kb.Policy)) {
	p := k.Policy()
	edit(&p)
	k.SetPolicy(p)
}
