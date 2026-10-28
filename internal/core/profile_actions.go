package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
)

func (m *Manager) profileIndex(id string) (int, error) {
	i := slices.IndexFunc(m.profiles.Items, func(p Profile) bool { return p.ID == id })
	if i < 0 {
		return -1, fmt.Errorf("配置不存在")
	}
	return i, nil
}

func (m *Manager) ReadProfile(id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.profileIndex(id); err != nil {
		return nil, err
	}
	return ReadConfig(m.profilePath(id))
}

func (m *Manager) RenameProfile(id, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.profileIndex(id)
	if err != nil {
		return err
	}
	name = cleanProfileName(name)
	if name == "" {
		return fmt.Errorf("配置名称不能为空")
	}
	for j, p := range m.profiles.Items {
		if j != i && p.Name == name {
			return fmt.Errorf("配置名称已存在")
		}
	}
	next := profileCatalog{Active: m.profiles.Active, Items: slices.Clone(m.profiles.Items)}
	next.Items[i].Name = name
	return m.saveProfiles(next)
}

func (m *Manager) DeleteProfile(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	i, err := m.profileIndex(id)
	if err != nil {
		return err
	}
	if m.proc != nil && id == m.profiles.Active {
		return fmt.Errorf("请先停止内核或切换配置，再删除当前配置")
	}
	old, path := m.profiles, m.profilePath(id)
	next := profileCatalog{Active: old.Active, Items: slices.Delete(slices.Clone(old.Items), i, i+1)}
	if next.Active == id {
		next.Active = ""
		if len(next.Items) > 0 {
			next.Active = next.Items[0].ID
		}
	}
	if err := m.saveProfiles(next); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.Join(fmt.Errorf("删除配置文件失败: %w", err), m.saveProfiles(old))
	}
	return nil
}

// ReplaceProfile publishes an immutable revision only after validation and, if active, startup.
// A crash before catalog publication leaves the previous config selected on the next launch.
func (m *Manager) ReplaceProfile(ctx context.Context, id string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.profileIndex(id)
	if err != nil {
		return err
	}
	if err := m.verifyKernel(ctx); err != nil {
		return err
	}
	revision, path, err := m.prepareProfile(ctx, data)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			os.Remove(path)
		}
	}()
	oldPath := m.profilePath(id)
	next := profileCatalog{Active: m.profiles.Active, Items: slices.Clone(m.profiles.Items)}
	next.Items[i].Revision = revision
	m.reap()
	if id == m.profiles.Active && m.proc != nil {
		err = m.activateCatalog(ctx, next)
	} else {
		err = m.saveProfiles(next)
	}
	if err != nil {
		return err
	}
	committed = true
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(&m.logs, "[Clash Go] 配置已更新，但旧修订文件清理失败；请检查数据目录权限")
	}
	return nil
}

func (m *Manager) prepareProfile(ctx context.Context, data []byte) (string, string, error) {
	normalized, err := Normalize(data, "", "", m.settings.Port)
	if err != nil {
		return "", "", err
	}
	if len(normalized) > MaxConfigSize {
		return "", "", fmt.Errorf("规范化后的配置不能超过 8 MiB")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(random[:])
	path := m.profilePath(id)
	if err := writePrivate(path, normalized); err != nil {
		return "", "", err
	}
	err = m.validate(ctx, m.kernelPath(), path)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		os.Remove(path)
		return "", "", err
	}
	return id, path, nil
}

// ExportFile uses the same private, atomic write as saved configuration, never truncating on failure.
func ExportFile(path string, data []byte) error { return writePrivate(path, data) }
