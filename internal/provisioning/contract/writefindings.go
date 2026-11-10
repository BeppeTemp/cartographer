package contract

// WriteFindingsHookName is the reserved hook name of the client-generated
// write-findings hook (D353).
const WriteFindingsHookName = "cartographer-write-findings"

// ReservedHookNames are the hook names the client generates itself: a KB hook
// carrying one is ignored with a warning (Apply), and a managed entry carrying
// one is never driven by the server manifest (ComputeDiff, Apply).
var ReservedHookNames = map[string]bool{
	BootstrapHookName:     true,
	WriteFindingsHookName: true,
}

func IsReservedHook(kind, name string) bool {
	return kind == "hook" && ReservedHookNames[name]
}
