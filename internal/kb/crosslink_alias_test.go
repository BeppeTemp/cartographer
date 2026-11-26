package kb

import (
	"reflect"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// aliasRig is kb-a, whose settings declare a relation named old-b to the remote
// wantRemote, linking an alias of kb-b, whose origin repo is bRepo ("" = none).
func aliasRig(t *testing.T, wantRemote, bRepo string) (a, b *KB) {
	t.Helper()
	deps := [][2]string{}
	if wantRemote != "" {
		deps = append(deps, [2]string{"old-b", wantRemote})
	}
	a = relKB(t, "a", deps...)
	b = relKB(t, bRepo)
	m := NewMounts(map[string]*KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, map[string]string{"old-b": "kb-b"})
	a.SetMounts(m)
	b.SetMounts(m)
	writeConcept(t, a, "p", "[[kb:old-b/ops/x]] [[kb:kb-b/ops/other]]\n")
	writeConcept(t, a, "q", "[[kb:old-b/ops/x]]\n")
	writeConcept(t, b, "ops/x", "x\n")
	writeConcept(t, b, "back", "[[kb:kb-a/p]]\n")
	return a, b
}

// AC-cross-kb-rewrite-14
func TestAliasGuardFollowsRelationRemote(t *testing.T) {
	alias := CrossLink{KB: "old-b", ID: "ops/x"}
	direct := CrossLink{KB: "kb-b", ID: "ops/x"}
	for _, c := range []struct {
		name, remote, bRepo string
		follow              bool
	}{
		{"right remote", "org/b", "b", true},
		{"other remote", "org/other", "b", false},
		{"target has no remote", "org/b", "", false},
		{"no relation of that name", "", "b", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			remote := ""
			if c.remote != "" {
				remote = "b"
				if c.remote == "org/other" {
					remote = "other"
				}
			}
			a, b := aliasRig(t, remote, c.bRepo)
			if got := a.CrossTarget(alias); got.Mounted != c.follow || got.Err != nil {
				t.Errorf("CrossTarget(alias) = %+v, want mounted %v", got, c.follow)
			}
			if got := a.CrossTarget(direct); !got.Mounted || !got.Exists {
				t.Errorf("a link by the current name has no guard: %+v", got)
			}
			names := a.Mounts().NamesFor("kb-b")
			pages, err := a.CrossLinksTo(names, map[string]bool{"ops/x": true})
			var want []okf.ConceptID
			if c.follow {
				want = []okf.ConceptID{"p", "q"}
			}
			if err != nil || !reflect.DeepEqual(pages, want) {
				t.Errorf("CrossLinksTo = %v, %v; want %v", pages, err, want)
			}
			// The same rule on the other side: kb-a's pages as kb-b's incoming.
			refs, err := b.CrossIncoming("back")
			if err != nil || len(refs) != 0 {
				t.Errorf("unrelated incoming: %v, %v", refs, err)
			}
			in, err := b.CrossIncoming("ops/x")
			if err != nil {
				t.Fatal(err)
			}
			var wantIn []CrossRef
			if c.follow {
				wantIn = []CrossRef{{KB: "kb-a", ID: "p"}, {KB: "kb-a", ID: "q"}}
			}
			if !reflect.DeepEqual(in, wantIn) {
				t.Errorf("CrossIncoming = %v, want %v", in, wantIn)
			}
		})
	}
}

func TestAliasGuardNoGitPerCall(t *testing.T) {
	old := relationsMaxAge
	relationsMaxAge = time.Hour
	t.Cleanup(func() { relationsMaxAge = old })
	a, _ := aliasRig(t, "b", "b")
	direct, alias := CrossLink{KB: "kb-b", ID: "ops/x"}, CrossLink{KB: "old-b", ID: "ops/x"}
	a.CrossTarget(direct)
	base := relationsGitReads.Load()
	for i := 0; i < 1000; i++ {
		a.CrossTarget(direct)
	}
	if n := relationsGitReads.Load() - base; n != 0 {
		t.Fatalf("%d git reads for links by the current name, want 0", n)
	}
	a.CrossTarget(alias) // the first alias link may read once
	base = relationsGitReads.Load()
	for i := 0; i < 1000; i++ {
		if !a.CrossTarget(alias).Mounted {
			t.Fatal("alias not followed")
		}
	}
	if n := relationsGitReads.Load() - base; n != 0 {
		t.Fatalf("%d git reads for alias links within relationsMaxAge, want 0", n)
	}
}

func TestAliasGuardRelationsError(t *testing.T) {
	a, _ := aliasRig(t, "b", "b")
	// An unreadable ref makes Relations fail: the answer is unknown, never "not mounted".
	gitHere(t, a.Root, "update-ref", "-d", "refs/remotes/origin/main")
	gitHere(t, a.Root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/gone")
	a.forgetRelationsValidation()
	if got := a.CrossTarget(CrossLink{KB: "old-b", ID: "ops/x"}); got.Err == nil || got.Mounted {
		t.Errorf("CrossTarget = %+v, want Err set and not Mounted", got)
	}
	if got := a.CrossTarget(CrossLink{KB: "kb-b", ID: "ops/x"}); got.Err != nil || !got.Mounted {
		t.Errorf("a link by the current name never reads Relations: %+v", got)
	}
}

// A page holding a guarded-off alias link and a link by the current name: only
// the followed one is retargeted (the names RewriteCrossLinks gets are the
// followed ones).
func TestRewriteSkipsGuardedAlias(t *testing.T) {
	a, _ := aliasRig(t, "other", "b") // relation old-b -> org/other, kb-b's origin is org/b
	names, err := a.FollowedNames("kb-b")
	if err != nil || !reflect.DeepEqual(names, []string{"kb-b"}) {
		t.Fatalf("FollowedNames = %v, %v", names, err)
	}
	moves := []CrossRetarget{{FromKB: "kb-b", FromID: "ops/x", ToKB: "kb-b", ToID: "ops/y"}}
	got, n := RewriteCrossLinks("[[kb:old-b/ops/x]] [[kb:kb-b/ops/x]]", []string{"kb-a"}, moves,
		func(string) []string { return names })
	if want := "[[kb:old-b/ops/x]] [[kb:kb-b/ops/y]]"; got != want || n != 1 {
		t.Errorf("got %q, %d; want %q, 1", got, n, want)
	}
	// With the right remote both names are followed.
	a2, _ := aliasRig(t, "b", "b")
	if names, _ := a2.FollowedNames("kb-b"); !reflect.DeepEqual(names, []string{"kb-b", "old-b"}) {
		t.Errorf("followed names = %v", names)
	}
}
