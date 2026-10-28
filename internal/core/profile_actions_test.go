package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProfileActions(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	id, err := m.AddProfile(ctx, "First", []byte(directConfig))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddProfile(ctx, "Second", []byte(directConfig))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " \n ", "Second"} {
		if err := m.RenameProfile(id, name); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	if err := m.RenameProfile(id, "New name"); err != nil {
		t.Fatal(err)
	}
	before, _ := m.ReadProfile(id)
	if err := m.ReplaceProfile(ctx, id, []byte("rules: [INVALID]")); err == nil {
		t.Fatal("invalid update accepted")
	}
	after, _ := m.ReadProfile(id)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid update changed config")
	}
	if err := m.ReplaceProfile(ctx, id, []byte(directConfig+"mode: direct\n")); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Running || m.Snapshot().Profiles[0].Revision == "" {
		t.Fatal("stopped update started process or revision missing")
	}
	reloaded, err := NewManager(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := reloaded.ReadProfile(id)
	if err != nil || !strings.Contains(string(data), "mode: direct") || reloaded.Snapshot().Profiles[0].Name != "New name" {
		t.Fatalf("revision not persisted: %s %v", data, err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProfile(id); err == nil {
		t.Fatal("deleted running config")
	}
	if err := m.ReplaceProfile(ctx, second, []byte(directConfig+"mode: global\n")); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().ActiveProfile != id || !m.Snapshot().Running {
		t.Fatal("inactive update interrupted current config")
	}
	if err := m.DeleteProfile(second); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProfile(id); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().HasConfig || len(m.Snapshot().Profiles) != 0 {
		t.Fatal("last profile still active")
	}
	for _, action := range []func() error{
		func() error { return m.DeleteProfile("../../settings") },
		func() error { return m.RenameProfile("../../settings", "x") },
		func() error { _, err := m.ReadProfile("../../settings"); return err },
		func() error { return m.ReplaceProfile(ctx, "../../settings", []byte(directConfig)) },
	} {
		if action() == nil {
			t.Fatal("unregistered profile accepted")
		}
	}
	if err := os.WriteFile(filepath.Join(m.dir, "config.yaml"), []byte(directConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().HasConfig {
		t.Fatal("orphaned legacy file resurrected")
	}
	if err := m.Start(ctx); err == nil {
		t.Fatal("started deleted legacy config")
	}
}

func TestProfileReplacementRollback(t *testing.T) {
	for _, cause := range []string{"startup", "catalog", "cancel"} {
		t.Run(cause, func(t *testing.T) {
			m := testManager(t)
			ctx := context.Background()
			id, err := m.AddProfile(ctx, "Original", []byte(directConfig))
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.SetMode(ctx, "global"); err != nil {
				t.Fatal(err)
			}
			if err := m.Select(ctx, "PROXY", "REJECT"); err != nil {
				t.Fatal(err)
			}
			original, _ := m.ReadProfile(id)
			data := []byte(directConfig + "mode: direct\n")
			switch cause {
			case "startup":
				data = []byte(directConfig + "hosts: {crash.test: 127.0.0.1}\n")
			case "catalog":
				path := filepath.Join(m.dir, "profiles.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := m.ReplaceProfile(ctx, id, data); err == nil {
				t.Fatal("failed replacement accepted")
			}
			after, _ := m.ReadProfile(id)
			if !bytes.Equal(original, after) || m.Snapshot().Profiles[0].Revision != "" {
				t.Fatal("original replaced")
			}
			if err := m.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := m.Snapshot()
			if !state.Running || state.Mode != "global" || state.Groups[0].Now != "REJECT" {
				t.Fatalf("old session lost: %+v", state)
			}
			files, _ := os.ReadDir(filepath.Join(m.dir, "profiles"))
			if len(files) != 1 {
				t.Fatal("failed revision leaked")
			}
		})
	}
}

func TestProfileReplacementRunning(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	id, err := m.AddProfile(ctx, "Live", []byte(directConfig))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceProfile(ctx, id, []byte(directConfig+"mode: direct\n")); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Mode != "direct" || !m.Snapshot().Running || m.Snapshot().ActiveProfile != id {
		t.Fatal("new configuration not applied")
	}
	files, _ := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if len(files) != 1 {
		t.Fatal("old revision not cleaned")
	}
}

func TestBackupRestoreAtomicAndAdditive(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	id, err := m.AddProfile(ctx, "A", []byte(directConfig))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddProfile(ctx, "B", []byte(directConfig+"mode: direct\n")); err != nil {
		t.Fatal(err)
	}
	data, err := m.BackupProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreProfiles(ctx, data); err != nil {
		t.Fatal(err)
	}
	state := m.Snapshot()
	if !state.Running || state.ActiveProfile != id || len(state.Profiles) != 4 || state.Profiles[2].Name != "A (2)" {
		t.Fatal("backup replaced active profiles")
	}
	var backup profileBackup
	json.Unmarshal(data, &backup)
	backup.Profiles[1].YAML = "rules: [INVALID]"
	bad, _ := json.Marshal(backup)
	before := m.Snapshot().Profiles
	filesBefore, _ := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if err := m.RestoreProfiles(ctx, bad); err == nil {
		t.Fatal("invalid backup accepted")
	}
	filesAfter, _ := os.ReadDir(filepath.Join(m.dir, "profiles"))
	if !reflect.DeepEqual(before, m.Snapshot().Profiles) || len(filesAfter) != len(filesBefore) {
		t.Fatal("partial backup published")
	}
	for _, bad := range [][]byte{[]byte(`{}`), []byte(`{"clashGoBackupVersion":2}`), bytes.Repeat([]byte(" "), MaxBackupSize+1)} {
		if err := m.RestoreProfiles(ctx, bad); err == nil {
			t.Fatal("bad backup accepted")
		}
	}
	empty := testManager(t)
	if err := empty.RestoreProfiles(ctx, data); err != nil {
		t.Fatal(err)
	}
	if empty.Snapshot().Running || !empty.Snapshot().HasConfig || empty.Snapshot().ActiveProfile != empty.Snapshot().Profiles[0].ID {
		t.Fatal("first restore selection incorrect")
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := ExportFile(path, data); err != nil {
		t.Fatal(err)
	}
	exported, err := ReadBackup(path)
	if err != nil || !bytes.Equal(data, exported) {
		t.Fatal("backup export mismatch")
	}
}

func TestRejectUnsafeRevisionCatalog(t *testing.T) {
	for _, revision := range []string{"../secret", "legacy", strings.Repeat("a", 31)} {
		dir := t.TempDir()
		data, _ := json.Marshal(profileCatalog{Active: "legacy", Items: []Profile{{ID: "legacy", Name: "A", Revision: revision}}})
		if err := os.WriteFile(filepath.Join(dir, "profiles.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewManager(dir); err == nil {
			t.Fatalf("accepted revision %s", revision)
		}
	}
}
