//go:build windows

package main

// watchReloadSignal has nothing to watch on Windows, which has no SIGHUP: the
// config file poll is the only trigger there. The nil channel never fires.
func watchReloadSignal() (<-chan struct{}, func()) { return nil, func() {} }
