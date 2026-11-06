package plugin

import (
	"errors"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// Status is where an installed plugin stands.
type Status string

const (
	StatusActive     Status = "active"
	StatusPending    Status = "pending approval"
	StatusDisabled   Status = "disabled"
	StatusNotFetched Status = "not fetched"
	StatusModified   Status = "modified"
	StatusInvalid    Status = "invalid"
)

// State is the one rule that decides whether a plugin runs: every consumer
// calls it and nothing else. A plugin is active only when it is enabled, the
// user approved a digest, and the stored tree of that digest is intact and still
// valid (for example the running version still satisfies its `cartographer`
// range). Only an active plugin yields a *Plugin. A newer Installed digest never
// affects it: the approved tree is what runs.
func State(name string, e clientconfig.PluginEntry, s Store, opts Options) (Status, *Plugin) {
	switch {
	case !e.Enabled:
		return StatusDisabled, nil
	case e.Approved == "":
		return StatusPending, nil
	}
	p, err := s.Open(name, e.Approved, opts)
	switch {
	case err == nil:
		return StatusActive, &p
	case errors.Is(err, ErrNotFetched):
		return StatusNotFetched, nil
	case errors.Is(err, ErrModified):
		return StatusModified, nil
	default:
		return StatusInvalid, nil
	}
}
