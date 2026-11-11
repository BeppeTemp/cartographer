package plugin

import "errors"

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

// Entry is the configuration of one plugin as State reads it: the client's
// `plugins` entry (clientconfig.PluginEntry) and the pin of a headless
// component (Pin) both implement it, so no component config is imported here.
type Entry interface {
	// PluginEnabled is false for a plugin the user switched off.
	PluginEnabled() bool
	// PluginApproved is the approved tree digest, "" while pending.
	PluginApproved() string
}

// State is the one rule that decides whether a plugin runs: every consumer
// calls it and nothing else. A plugin is active only when it is enabled, the
// user approved a digest, and the stored tree of that digest is intact and still
// valid (for example the running version still satisfies its `cartographer`
// range). Only an active plugin yields a *Plugin. A newer Installed digest never
// affects it: the approved tree is what runs.
func State(name string, e Entry, s Store, opts Options) (Status, *Plugin) {
	switch {
	case !e.PluginEnabled():
		return StatusDisabled, nil
	case e.PluginApproved() == "":
		return StatusPending, nil
	}
	p, err := s.Open(name, e.PluginApproved(), opts)
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
