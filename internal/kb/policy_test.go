package kb

import "testing"

// D775: a snapshot never aliases the stored policy, nor the caller's slice.
func TestPolicySnapshotDoesNotAlias(t *testing.T) {
	k := &KB{}
	if got := k.Policy(); got.WriteGate != "" || got.AutoRepair != nil || got.AllowArtifactWrite {
		t.Fatalf("unset policy = %+v, want zero", got)
	}
	in := Policy{AutoRepair: []string{"a", "b"}, WriteGate: "error"}
	k.SetPolicy(in)
	in.AutoRepair[0] = "mutated-after-set"
	got := k.Policy()
	if got.AutoRepair[0] != "a" {
		t.Fatalf("SetPolicy kept the caller's slice: %v", got.AutoRepair)
	}
	got.AutoRepair[1] = "mutated-snapshot"
	if again := k.Policy(); again.AutoRepair[1] != "b" {
		t.Fatalf("Policy returned the stored slice: %v", again.AutoRepair)
	}
}
