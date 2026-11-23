// Package landertest holds the tests of the scripts that queue, check and land a change (the
// lander, signoff, ready, requeue, status, the triage and the profiles they read), apart from
// internal/repodocs so that `go test` runs them in parallel with the documentation checks: the
// lander tests alone were most of that package's run time. The package has no code of its own.
package landertest
