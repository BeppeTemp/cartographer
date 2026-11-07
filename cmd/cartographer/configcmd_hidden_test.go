package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// The plugins key is a visible schema row, so `cartographer config` lists it.
func TestClientEntriesListPlugins(t *testing.T) {
	found := false
	for _, e := range clientEntries(t.TempDir()) {
		found = found || e.Key == "plugins"
	}
	if !found {
		t.Error("config does not list the plugins key")
	}
	if k, _ := clientconfig.Schema.Lookup("plugins"); k.Hidden {
		t.Error("plugins is still hidden")
	}
}
