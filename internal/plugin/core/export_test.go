package core

// NameRe lets the external tests keep another package's copy of the name rule equal.
var NameRe = nameRe

// WithoutPoints empties the point registry for the test, the state of a binary
// that links no client loaders, and restores it on cleanup.
func WithoutPoints(t interface{ Cleanup(func()) }) {
	saved := points
	points = map[string]Point{}
	t.Cleanup(func() { points = saved })
}
