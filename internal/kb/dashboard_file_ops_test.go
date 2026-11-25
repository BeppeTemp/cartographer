package kb

import (
	"encoding/json"
	"reflect"
	"testing"
)

const fileOpsDashboard = `title: Week
description: What moves this week.
widgets:
  - id: blocked
    type: kanban
    title: Blocked
    width: 2
    filter:
      status: [blocked]
    group_by: status
  - id: intro
    type: note
    text: hello
`

func TestDashboardFileOpsRoundTrip(t *testing.T) {
	t.Parallel()
	title, desc, ops, err := DashboardFileOps([]byte(fileOpsDashboard))
	if err != nil {
		t.Fatal(err)
	}
	if title != "Week" || desc != "What moves this week." {
		t.Errorf("title %q description %q", title, desc)
	}
	if len(ops) != 4 || ops[0].Op != DashboardOpSetTitle || ops[1].Op != DashboardOpSetDescription || ops[2].Op != DashboardOpAdd {
		t.Fatalf("ops = %+v", ops)
	}
	// The ops travel as JSON through dashboard_patch: decode them as the tool does.
	raw, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDashboardOps(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplyDashboardOps(nil, decoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDashboard(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ParseDashboard([]byte(fileOpsDashboard))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestDashboardFileOpsRefusesABadWidget(t *testing.T) {
	t.Parallel()
	if _, _, _, err := DashboardFileOps([]byte("widgets:\n  - id: a\n    type: nope\n")); err == nil {
		t.Error("a widget with a problem must not become an op")
	}
}
