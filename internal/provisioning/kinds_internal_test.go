package provisioning

import (
	"slices"
	"testing"
)

// A kind added to the matrix (artifactKinds) without a descriptor would be
// invisible on the Atlas Artifacts page (D864); a descriptor that claims it is
// not KB-only without a matrix row would promise a sync that never happens.
func TestKindsMatchTheMatrix(t *testing.T) {
	described := map[string]KindInfo{}
	for _, k := range Kinds() {
		if _, dup := described[k.Kind]; dup {
			t.Errorf("kind %q has more than one descriptor", k.Kind)
		}
		described[k.Kind] = k
		if k.Title == "" || k.KBPath == "" || k.Doc == "" {
			t.Errorf("kind %q has an empty field: %+v", k.Kind, k)
		}
		inMatrix := slices.Contains(artifactKinds, k.Kind)
		if n := countTrue(inMatrix, k.KBOnly, k.Declarative); n != 1 {
			t.Errorf("kind %q: want exactly one of in-matrix/KBOnly/Declarative, got matrix=%v KBOnly=%v Declarative=%v", k.Kind, inMatrix, k.KBOnly, k.Declarative)
		}
		if k.KBOnly && len(Destinations(k.Kind, "")) != 0 {
			t.Errorf("kind %q is KB-only yet the matrix delivers it to %v", k.Kind, Destinations(k.Kind, ""))
		}
		if k.Declarative && len(Destinations(k.Kind, "")) != 0 {
			t.Errorf("kind %q is declarative yet the matrix delivers it", k.Kind)
		}
		if !k.KBOnly && !k.Declarative && len(Destinations(k.Kind, "")) == 0 {
			t.Errorf("kind %q is synced yet no client receives it", k.Kind)
		}
	}
	for _, kind := range artifactKinds {
		if _, ok := described[kind]; !ok {
			t.Errorf("matrix kind %q has no descriptor in Kinds()", kind)
		}
	}
}

// Kinds hands out a copy: a caller sorting or editing it must not reorder what
// the next caller reads.
func TestKindsReturnsACopy(t *testing.T) {
	first := Kinds()
	first[0].Title = "changed"
	if Kinds()[0].Title == "changed" {
		t.Fatal("Kinds() shares its backing array with the caller")
	}
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}
