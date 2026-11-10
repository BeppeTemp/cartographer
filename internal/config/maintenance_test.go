package config

import "testing"

// maintenance.auto_repair follows env > YAML > default, default true: absent
// from YAML must differ from an explicit false.
func TestMaintenanceAutoRepairPrecedence(t *testing.T) {
	if !Default().Maintenance.AutoRepair {
		t.Fatal("the background repair is on by default")
	}
	cfg, err := Load(writeCfg(t, "maintenance:\n  auto_repair: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Maintenance.AutoRepair {
		t.Error("an explicit `maintenance.auto_repair: false` must survive the default")
	}
	bare, err := Load(writeCfg(t, "http: \":39273\"\n"))
	if err != nil || !bare.Maintenance.AutoRepair {
		t.Fatalf("absent maintenance.auto_repair must keep the default: %v", err)
	}
	t.Setenv("CARTOGRAPHER_MAINTENANCE_AUTO_REPAIR", "false")
	if err := FromEnv(bare); err != nil {
		t.Fatal(err)
	}
	if bare.Maintenance.AutoRepair {
		t.Error("CARTOGRAPHER_MAINTENANCE_AUTO_REPAIR=false must win over the default")
	}
	if bare.OriginOf("maintenance.auto_repair") != OriginEnv {
		t.Errorf("origin = %v, want env", bare.OriginOf("maintenance.auto_repair"))
	}
	if v, _ := bare.serverValue("maintenance.auto_repair"); v != false {
		t.Errorf("serverValue = %v", v)
	}
	t.Setenv("CARTOGRAPHER_MAINTENANCE_AUTO_REPAIR", "true")
	if err := FromEnv(cfg); err != nil || !cfg.Maintenance.AutoRepair {
		t.Errorf("the env true must win over YAML false: %v", err)
	}
}

// The per-KB auto_repair stays retired (D907); the server-level key is a real
// one. The setting switches the timer, not a KB's policy.
func TestMaintenanceAutoRepairIsServerLevelOnly(t *testing.T) {
	retired := false
	for _, r := range Retired.Removed {
		if r.Path == "kbs[].auto_repair" {
			retired = true
		}
		if r.Path == "maintenance.auto_repair" {
			t.Error("maintenance.auto_repair must not be retired")
		}
	}
	if !retired {
		t.Error("kbs[].auto_repair must stay retired")
	}
	found := false
	for _, k := range Schema.Keys {
		if k.Path == "maintenance.auto_repair" {
			found = true
		}
	}
	if !found {
		t.Error("maintenance.auto_repair has no schema row")
	}
}
