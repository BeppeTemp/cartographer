package kb

// SetReadRawHook installs fn as the ReadRaw observer for the duration of a
// test and returns the function that removes it (D248).
func SetReadRawHook(fn func(relPath string)) (restore func()) {
	prev := readRawHook
	readRawHook = fn
	return func() { readRawHook = prev }
}
