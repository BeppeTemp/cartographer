package lint

import "testing"

// AutoRepairOrder is a literal because its order matters (a page needs its
// template bound before its sections are renamed), but its members are exactly
// the checks whose registered fix may run with no person (D354, D355, D907): a
// check added to the registry as safe fails here until someone places it.
func TestAutoRepairOrderIsTheAutoRepairSafeSet(t *testing.T) {
	in := map[string]bool{}
	for _, check := range AutoRepairChecks() {
		in[check] = true
		if !AutoRepairSafe(check) {
			t.Errorf("%s is in AutoRepairOrder but its lint spec is not AutoRepairSafe", check)
		}
		if !isFixable(check) {
			t.Errorf("%s is in AutoRepairOrder but has no mechanical fix", check)
		}
	}
	for _, s := range Checks() {
		if s.AutoRepairSafe && !in[s.Name] {
			t.Errorf("%s is AutoRepairSafe but missing from AutoRepairOrder", s.Name)
		}
	}
}

func TestAutoRepairChecksReturnsACopy(t *testing.T) {
	a := AutoRepairChecks()
	a[0] = "mutated"
	if AutoRepairChecks()[0] == "mutated" {
		t.Fatal("AutoRepairChecks shares its backing array")
	}
}

func isFixable(name string) bool {
	for _, f := range FixableChecks {
		if f == name {
			return true
		}
	}
	return false
}
