package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A hidden schema row (the plugins key, until it is turned on) is not an entry
// of `cartographer config`.
func TestClientEntriesSkipHiddenKeys(t *testing.T) {
	for _, e := range clientEntries(t.TempDir()) {
		if e.Key == "plugins" {
			t.Errorf("config lists the hidden key: %+v", e)
		}
	}
	if k, _ := clientconfig.Schema.Lookup("plugins"); !k.Hidden {
		t.Error("plugins is not hidden")
	}
}
