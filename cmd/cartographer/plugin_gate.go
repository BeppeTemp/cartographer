package main

// pluginsEnabled says whether the plugin flags of `setup` and `connect`
// (--plugin, --approve-plugins) exist. They are not defined, and so not printed
// in a usage text or by -h, until the turn-on change of the plugins plan flips
// this; the tests set it. Nothing else of the plugin host is gated here: the
// `plugin` command is dispatched but unlisted (main.go).
var pluginsEnabled = func() bool { return false }
