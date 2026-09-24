//go:build windows

package provisioning

// hostWindows is the build-tagged half of hookHostWindows (hookshell.go): a
// file pair rather than a runtime.GOOS branch (docs/conventions.md
// §Dependencies).
const hostWindows = true
