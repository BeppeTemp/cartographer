//go:build !darwin

package tray

// SingleInstance has no work off macOS: Windows takes its mutex inside Run,
// and there is no tray elsewhere.
func SingleInstance() (release func(), second bool) { return func() {}, false }
