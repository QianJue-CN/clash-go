package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision string `json:"revision,omitempty"`
}

type profileCatalog struct {
	Active string    `json:"active"`
	Items  []Profile `json:"profiles"`
}

func (m *Manager) profilePath(id string) string {
	for _, profile := range m.profiles.Items {
		if profile.ID == id && profile.Revision != "" {
			id = profile.Revision
			break
		}
	}
	if id == "legacy" || id == "" {
		return filepath.Join(m.dir, "config.yaml")
	}
	return filepath.Join(m.dir, "profiles", id+".yaml")
}
func (m *Manager) configPath() string { return m.profilePath(m.profiles.Active) }

func (m *Manager) loadProfiles() error {
	if err := os.MkdirAll(filepath.Join(m.dir, "profiles"), 0700); err != nil {
		return err
	}
	data, err := ReadConfig(filepath.Join(m.dir, "profiles.json"))
	if os.IsNotExist(err) {
		if _, err := os.Stat(m.profilePath("legacy")); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		// Keep the original single config in place; only add a catalog entry for it.
		return m.saveProfiles(profileCatalog{Active: "legacy", Items: []Profile{{ID: "legacy", Name: "原有配置"}}})
	}
	if err != nil {
		return err
	}
	var catalog profileCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return fmt.Errorf("读取订阅列表失败: %w", err)
	}
	seen := map[string]bool{}
	files := map[string]bool{}
	for i, profile := range catalog.Items {
		id, err := hex.DecodeString(profile.ID)
		if profile.ID != "legacy" && (err != nil || len(id) != 16) {
			return fmt.Errorf("订阅列表包含无效 ID")
		}
		if profile.Revision != "" {
			revision, err := hex.DecodeString(profile.Revision)
			if err != nil || len(revision) != 16 {
				return fmt.Errorf("配置列表包含无效修订 ID")
			}
		}
		if seen[profile.ID] {
			return fmt.Errorf("订阅列表包含重复 ID")
		}
		seen[profile.ID] = true
		fileID := profile.ID
		if profile.Revision != "" {
			fileID = profile.Revision
		}
		if files[fileID] {
			return fmt.Errorf("配置列表包含重复文件引用")
		}
		files[fileID] = true
		catalog.Items[i].Name = cleanProfileName(profile.Name)
		if catalog.Items[i].Name == "" {
			return fmt.Errorf("订阅名称不能为空")
		}
	}
	if (len(catalog.Items) > 0 && !seen[catalog.Active]) || (len(catalog.Items) == 0 && catalog.Active != "") {
		return fmt.Errorf("当前订阅记录无效")
	}
	m.profiles = catalog
	return nil
}

func (m *Manager) saveProfiles(catalog profileCatalog) error {
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	if len(data) > MaxConfigSize {
		return fmt.Errorf("订阅列表过大")
	}
	if err := writePrivate(filepath.Join(m.dir, "profiles.json"), data); err != nil {
		return err
	}
	m.profiles = catalog
	return nil
}

func (catalog profileCatalog) profileName(name string) string {
	name = cleanProfileName(name)
	exists := func(name string) bool {
		return slices.ContainsFunc(catalog.Items, func(p Profile) bool { return p.Name == name })
	}
	if name == "" {
		for n := 1; ; n++ {
			fallback := fmt.Sprintf("订阅 %d", n)
			if !exists(fallback) {
				return fallback
			}
		}
	}
	if !exists(name) {
		return name
	}
	prefix := []rune(name)
	if len(prefix) > 64 {
		prefix = prefix[:64]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)", string(prefix), n)
		if !exists(candidate) {
			return candidate
		}
	}
}

// AddProfile stores a validated independent config without interrupting the current one.
// The first profile becomes selected, but starting the process is a separate operation.
func (m *Manager) AddProfile(ctx context.Context, name string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addProfile(ctx, name, data, false)
}

func (m *Manager) addProfile(ctx context.Context, name string, data []byte, activate bool) (string, error) {
	if err := m.verifyKernel(ctx); err != nil {
		return "", err
	}
	id, path, err := m.prepareProfile(ctx, data)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			os.Remove(path)
		}
	}()
	next := profileCatalog{Active: m.profiles.Active, Items: slices.Clone(m.profiles.Items)}
	next.Items = append(next.Items, Profile{ID: id, Name: next.profileName(name)})
	if next.Active == "" || activate {
		next.Active = id
	}
	if err := m.saveProfiles(next); err != nil {
		return "", err
	}
	committed = true
	return id, nil
}

// SwitchProfile starts the chosen config. The persisted selection changes only on success.
func (m *Manager) SwitchProfile(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.profiles.Items, func(p Profile) bool { return p.ID == id }) {
		return fmt.Errorf("订阅不存在")
	}
	if err := m.verifyKernel(ctx); err != nil {
		return err
	}
	m.reap()
	if id == m.profiles.Active && m.proc != nil {
		return nil
	}
	if err := m.validate(ctx, m.kernelPath(), m.profilePath(id)); err != nil {
		return err
	}
	next := m.profiles
	next.Active = id
	return m.activateCatalog(ctx, next)
}

func (m *Manager) activateCatalog(ctx context.Context, next profileCatalog) error {
	if err := m.refresh(ctx); err != nil {
		return fmt.Errorf("无法保存当前运行状态，取消切换: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	oldCatalog, oldState := m.profiles, m.state
	if err := m.stop(); err != nil {
		return err
	}
	m.profiles = next
	if err := m.start(ctx, m.kernelPath()); err != nil {
		return m.rollbackProfile(ctx, oldCatalog, oldState, err)
	}
	if err := m.refresh(ctx); err != nil {
		return m.rollbackProfile(ctx, oldCatalog, oldState, err)
	}
	if err := ctx.Err(); err != nil {
		return m.rollbackProfile(ctx, oldCatalog, oldState, err)
	}
	if err := m.saveProfiles(m.profiles); err != nil {
		return m.rollbackProfile(ctx, oldCatalog, oldState, err)
	}
	return nil
}

func (m *Manager) rollbackProfile(ctx context.Context, catalog profileCatalog, state Snapshot, cause error) error {
	m.profiles = catalog
	if err := m.stop(); err != nil {
		return errors.Join(cause, fmt.Errorf("停止新配置失败: %w", err))
	}
	if !state.Running || ctx.Err() != nil {
		return cause
	}
	err := m.start(ctx, m.kernelPath())
	if err == nil {
		err = m.restoreSession(ctx, state)
	}
	if err != nil {
		return errors.Join(cause, fmt.Errorf("已保留原配置，但恢复运行失败: %w", err))
	}
	return fmt.Errorf("切换失败，已恢复原配置运行: %w", cause)
}
