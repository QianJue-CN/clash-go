package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
)

const MaxBackupSize = 32 << 20

type profileBackup struct {
	Version  int             `json:"clashGoBackupVersion"`
	Active   int             `json:"active"`
	Profiles []backupProfile `json:"profiles"`
}

type backupProfile struct {
	Name string `json:"name"`
	YAML string `json:"yaml"`
}

func (m *Manager) BackupProfiles() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.profiles.Items) > 1000 {
		return nil, fmt.Errorf("配置备份最多支持 1000 项")
	}
	backup := profileBackup{Version: 1, Active: -1}
	size := 0
	for i, profile := range m.profiles.Items {
		data, err := ReadConfig(m.profilePath(profile.ID))
		if err != nil {
			return nil, err
		}
		size += len(data)
		if size > MaxBackupSize {
			return nil, fmt.Errorf("配置备份不能超过 32 MiB")
		}
		backup.Profiles = append(backup.Profiles, backupProfile{Name: profile.Name, YAML: string(data)})
		if profile.ID == m.profiles.Active {
			backup.Active = i
		}
	}
	if len(backup.Profiles) == 0 {
		return nil, fmt.Errorf("没有可备份的配置")
	}
	data, err := json.Marshal(backup)
	if len(data) > MaxBackupSize {
		return nil, fmt.Errorf("配置备份不能超过 32 MiB")
	}
	return data, err
}

func ReadBackup(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxBackupSize+1))
	if err == nil && len(data) > MaxBackupSize {
		return nil, fmt.Errorf("配置备份不能超过 32 MiB")
	}
	return data, err
}

// RestoreProfiles appends validated configs in one catalog commit; it never replaces existing data.
func (m *Manager) RestoreProfiles(ctx context.Context, data []byte) error {
	if len(data) > MaxBackupSize {
		return fmt.Errorf("配置备份不能超过 32 MiB")
	}
	var backup profileBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return fmt.Errorf("无效 Clash Go 配置备份")
	}
	if backup.Version != 1 || len(backup.Profiles) == 0 || len(backup.Profiles) > 1000 || backup.Active < 0 || backup.Active >= len(backup.Profiles) {
		return fmt.Errorf("不支持的备份版本或无效配置列表（最多 1000 项）")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyKernel(ctx); err != nil {
		return err
	}
	next := profileCatalog{Active: m.profiles.Active, Items: slices.Clone(m.profiles.Items)}
	var paths []string
	committed := false
	defer func() {
		if !committed {
			for _, path := range paths {
				os.Remove(path)
			}
		}
	}()
	for i, profile := range backup.Profiles {
		if cleanProfileName(profile.Name) == "" {
			return fmt.Errorf("备份中的配置名称不能为空")
		}
		id, path, err := m.prepareProfile(ctx, []byte(profile.YAML))
		if err != nil {
			return fmt.Errorf("备份中第 %d 个配置校验失败: %w", i+1, err)
		}
		paths = append(paths, path)
		next.Items = append(next.Items, Profile{ID: id, Name: next.profileName(profile.Name)})
		if m.profiles.Active == "" && i == backup.Active {
			next.Active = id
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.saveProfiles(next); err != nil {
		return err
	}
	committed = true
	return nil
}
