package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// A stable route declares the Go type of its body.
func TestAPIRoutes_StableRoutesAreTyped(t *testing.T) {
	t.Parallel()
	for _, rt := range apiRoutes {
		if rt.Tier == tierStable && rt.Response == nil {
			t.Errorf("stable route %s has no Response type", rt.Pattern)
		}
	}
}

// The live body of each stable route decodes into its declared type with no
// unknown field: a key the handler writes and the type lacks fails here.
func TestAPIRoutes_BodiesDecodeIntoTheirResponseType(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), false, true)
	query := map[string]string{"/kbs/{kb}/concept": "?id=visible/alpha", "/kbs/{kb}/search": "?q=alpha"}
	for _, rt := range apiRoutes {
		if rt.Tier != tierStable || rt.Events != nil { // a stream never ends: apistream_test.go
			continue
		}
		path := strings.ReplaceAll(APIPrefix+rt.Pattern, "{kb}", "docs") + query[rt.Pattern]
		rr := getUI(t, h, path, "")
		if rr.Code != http.StatusOK {
			t.Errorf("%s: %d %s", rt.Pattern, rr.Code, rr.Body.String())
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(rr.Body.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(reflect.New(reflect.TypeOf(rt.Response)).Interface()); err != nil {
			t.Errorf("%s: body does not decode into %T: %v", rt.Pattern, rt.Response, err)
		}
	}
}

// Typing the bodies must not move a byte on the wire: these four used to be
// maps, which marshal with sorted keys. remarshaled is raw decoded into a map
// and encoded again, i.e. with every object's keys sorted.
func remarshaled(t *testing.T, raw []byte, top bool) (got, sorted string) {
	t.Helper()
	var c bytes.Buffer
	if err := json.Compact(&c, bytes.TrimSpace(raw)); err != nil {
		t.Fatal(err)
	}
	var v any
	if top {
		v = &map[string]json.RawMessage{}
	} else {
		v = new(any)
	}
	if err := json.Unmarshal(c.Bytes(), v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return c.String(), string(out)
}

func TestAPIRoutes_ConvertedBodiesKeepSortedKeys(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), true, false)
	get := func(p string) []byte { return getUI(t, h, UIAPIPrefix+p, "").Body.Bytes() }
	// Whole body sorted at every level.
	for _, p := range []string{"/whoami", "/kbs/docs/concept?id=visible/alpha"} {
		if got, sorted := remarshaled(t, get(p), false); got != sorted {
			t.Errorf("%s: wire order changed:\n%s\nwant\n%s", p, got, sorted)
		}
	}
	// Top level sorted (the rows inside keep their struct order, as before).
	for _, p := range []string{"/kbs", "/kbs/docs/overview"} {
		if got, sorted := remarshaled(t, get(p), true); got != sorted {
			t.Errorf("%s: top-level order changed:\n%s\nwant\n%s", p, got, sorted)
		}
	}
	var ov map[string]json.RawMessage
	if err := json.Unmarshal(get("/kbs/docs/overview"), &ov); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"concepts", "lint"} {
		if got, sorted := remarshaled(t, ov[k], false); got != sorted {
			t.Errorf("overview.%s: order changed: %s", k, got)
		}
	}
}
