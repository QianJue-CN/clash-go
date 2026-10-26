package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPersistenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings() != (Settings{Port: DefaultMixedPort}) {
		t.Fatal("wrong defaults")
	}
	want := Settings{Port: 17890}
	if err := m.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Settings{{Port: 0}, {Port: 80}, {Port: 65536}} {
		if err := m.SaveSettings(invalid); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
	reopened, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Settings() != want || reopened.Snapshot().Port != want.Port {
		t.Fatal("settings not preserved")
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"binary":"/legacy/mihomo","port":17890}`), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := NewManager(dir)
	if err != nil || migrated.Settings() != want {
		t.Fatalf("legacy migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(dir); err == nil {
		t.Fatal("corrupted settings silently reset")
	}
}
