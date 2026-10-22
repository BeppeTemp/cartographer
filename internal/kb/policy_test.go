package kb

import "testing"

// D775: a snapshot never aliases the stored policy.
func TestPolicySnapshotDoesNotAlias(t *testing.T) {
	k := &KB{}
	if got := k.Policy(); got.UsageStaleDays != 0 {
		t.Fatalf("unset policy = %+v, want zero", got)
	}
	k.SetPolicy(Policy{UsageStaleDays: 30})
	got := k.Policy()
	got.UsageStaleDays = 99
	if again := k.Policy(); again.UsageStaleDays != 30 {
		t.Fatalf("Policy returned the stored value: %+v", again)
	}
}
