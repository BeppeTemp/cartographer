package main

// ownsConsoleAlone reports whether a console with n attached processes is one
// Windows created for this process alone (detachOwnConsole, D1479): exactly one
// means nobody else — no shell — shares it.
func ownsConsoleAlone(n uint32) bool { return n == 1 }
