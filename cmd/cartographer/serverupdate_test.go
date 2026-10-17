package main

import (
	"context"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// D862: the release history is a second GitHub call, so it obeys the switch
// of the first. Every way of turning the update check off leaves the source
// nil, which the route answers as "disabled" without calling anything.
func TestServerReleasesSourceFollowsTheUpdateCheckSwitch(t *testing.T) {
	var calls int
	var gotETag, gotCurrent string
	old := serverReleasesFn
	serverReleasesFn = func(_ context.Context, _ updatecheck.Options, req updatecheck.ReleasesRequest) (updatecheck.ReleasesResult, error) {
		calls++
		gotETag, gotCurrent = req.ETag, req.Current
		return updatecheck.ReleasesResult{ETag: `"e2"`}, nil
	}
	t.Cleanup(func() { serverReleasesFn = old })
	t.Setenv(updatecheck.EnvDisable, "")

	on := config.Default()
	on.UpdateCheck = true
	off := config.Default()
	off.UpdateCheck = false

	for name, c := range map[string]struct {
		cfg     *config.Config
		current string
		env     string
	}{
		"config off": {off, "v0.16.1", ""},
		"dev build":  {on, "dev", ""},
		"no version": {on, "", ""},
		"env off":    {on, "v0.16.1", "1"},
		"no config":  {nil, "v0.16.1", ""},
	} {
		t.Setenv(updatecheck.EnvDisable, c.env)
		if src := serverReleasesSource(c.cfg, c.current); src != nil {
			t.Errorf("%s: a source was installed, the switch is off", name)
		}
	}
	if calls != 0 {
		t.Fatalf("the release fetch ran %d time(s) with the switch off", calls)
	}

	t.Setenv(updatecheck.EnvDisable, "")
	src := serverReleasesSource(on, "v0.16.1")
	if src == nil {
		t.Fatal("update check on, release build: the source must be installed")
	}
	res, err := src(context.Background(), `"e1"`)
	if err != nil || res.ETag != `"e2"` || calls != 1 || gotETag != `"e1"` || gotCurrent != "v0.16.1" {
		t.Errorf("res %+v err %v calls %d etag %q current %q", res, err, calls, gotETag, gotCurrent)
	}
}
