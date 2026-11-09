//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCronInstall_RefusesASymlinkedDefinition(t *testing.T) {
	e := newCronEnv(t)
	def, err := CronServerDefinitionPath()
	if err != nil {
		t.Fatal(err)
	}
	victim := victimFile(t)
	symlinkOrSkip(t, victim, def)
	_, err = e.m.Install(InstallOptions{DataDir: filepath.Join(e.home, "data"), HTTPAddr: "127.0.0.1:39999"})
	if err == nil || !strings.Contains(err.Error(), def) {
		t.Fatalf("Install error = %v, want one naming %s", err, def)
	}
	assertKept(t, victim)
}

func TestCronSyncInstall_RefusesASymlinkedParent(t *testing.T) {
	e := newCronEnv(t)
	def, err := CronSyncDefinitionPath()
	if err != nil {
		t.Fatal(err)
	}
	realDir := t.TempDir()
	victim := filepath.Join(realDir, filepath.Base(def))
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, realDir, filepath.Dir(def))
	err = e.m.InstallSyncTimer(45 * time.Minute)
	if err == nil || !strings.Contains(err.Error(), filepath.Dir(def)) {
		t.Fatalf("InstallSyncTimer error = %v, want one naming %s", err, filepath.Dir(def))
	}
	assertKept(t, victim)
}

func TestCronUninstall_RefusesASymlinkedDefinition(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	def, _ := CronServerDefinitionPath()
	victim := victimFile(t)
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, victim, def)
	err := e.m.Uninstall(false)
	if err == nil || !strings.Contains(err.Error(), def) {
		t.Fatalf("Uninstall error = %v, want one naming %s", err, def)
	}
	assertKept(t, victim)
	if _, lerr := os.Lstat(def); lerr != nil {
		t.Errorf("the planted link was removed: %v", lerr)
	}
}
