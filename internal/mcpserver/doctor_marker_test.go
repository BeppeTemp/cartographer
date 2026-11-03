package mcpserver

import (
	"testing"
	"time"
)

// #1503 WP10: last_doctor only moves on a doctor session's own closing line.
func TestLastDoctorDate_OnlyTheSkillsLine(t *testing.T) {
	t.Parallel()
	k, _ := repairKB(t, 1)
	at := func(day int) time.Time { return time.Date(2026, 3, day, 10, 0, 0, 0, time.UTC) }
	for i, text := range []string{
		"concept_write: tools/kb-doctor-checklist",
		"note: kb-doctor tomorrow",
		"Session notes\n\nkb-doctor: not on the first line",
	} {
		if err := k.AppendLog(text, at(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := lastDoctorDate(k); got != "" {
		t.Fatalf("a mention moved last_doctor: %q", got)
	}
	if err := k.AppendLog("kb-doctor: findings 5 -> 0", at(20)); err != nil {
		t.Fatal(err)
	}
	if err := k.AppendLog("concept_write: tools/kb-doctor-checklist", at(25)); err != nil {
		t.Fatal(err)
	}
	if got := lastDoctorDate(k); got != "2026-03-20" {
		t.Fatalf("last_doctor = %q, want the session's day", got)
	}
}
