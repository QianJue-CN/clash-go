package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type installedKernel struct {
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

func (m *Manager) kernelDir() string { return filepath.Join(m.dir, "kernels") }
func (m *Manager) kernelPath() string {
	if m.kernel.File == "" {
		return ""
	}
	return filepath.Join(m.kernelDir(), m.kernel.File)
}
func (m *Manager) loadKernel() error {
	if err := os.MkdirAll(m.kernelDir(), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "kernel.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var kernel installedKernel
	if err := json.Unmarshal(data, &kernel); err != nil {
		return fmt.Errorf("读取内核记录失败: %w", err)
	}
	if _, err := versionParts(kernel.Version); err != nil {
		return err
	}
	if !strings.HasPrefix(kernel.File, "mihomo-") || strings.ContainsAny(kernel.File, "/\\:") || filepath.Base(kernel.File) != kernel.File {
		return fmt.Errorf("内核记录包含不安全的文件名")
	}
	if _, err := parseHash(kernel.SHA256); err != nil {
		return err
	}
	m.kernel = kernel
	return nil
}

func probeKernel(ctx context.Context, binary, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, err := coreCommand(ctx, binary, "-v")
	if err != nil {
		return err
	}
	var output logBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("内核自检失败: %w", err)
	}
	fields := strings.Fields(output.String())
	if len(fields) < 3 || fields[0] != "Mihomo" || fields[1] != "Meta" || fields[2] != version {
		return fmt.Errorf("内核版本自检与发行版 %s 不符", version)
	}
	return nil
}

func (m *Manager) verifyKernel(ctx context.Context) error {
	if m.kernel.File == "" {
		return fmt.Errorf("内核尚未安装，请在设置中点击下载/更新内核")
	}
	digest, err := hashFile(m.kernelPath())
	if err != nil {
		return err
	}
	if digest != m.kernel.SHA256 {
		return fmt.Errorf("本地内核校验失败，请重新下载")
	}
	return probeKernel(ctx, m.kernelPath(), m.kernel.Version)
}

// Initialize installs once; later launches work offline with the verified cached core.
// An existing user config is started automatically; no direct-only config is fabricated.
func (m *Manager) Initialize(ctx context.Context, progress func(string)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if progress == nil {
		progress = func(string) {}
	}
	progress("正在检查已安装内核…")
	if err := m.verifyKernel(ctx); err != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.updateKernel(ctx, progress); err != nil {
			return err
		}
	}
	if m.profiles.Active == "" {
		return nil
	}
	if _, err := os.Stat(m.configPath()); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	m.reap()
	if m.proc != nil {
		return nil
	}
	progress("正在自动启动内核…")
	return m.start(ctx, m.kernelPath())
}

func (m *Manager) UpdateKernel(ctx context.Context, progress func(string)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if progress == nil {
		progress = func(string) {}
	}
	return m.updateKernel(ctx, progress)
}

func (m *Manager) updateKernel(ctx context.Context, progress func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.reap()
	progress("正在检查最新稳定版…")
	defer m.downloads.CloseIdleConnections()
	release, asset, err := latestKernel(ctx, m.downloads, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := m.verifyKernel(ctx); err == nil {
		current, _ := versionParts(m.kernel.Version)
		latest, _ := versionParts(release.Version)
		if slices.Compare(current[:], latest[:]) >= 0 {
			progress("已安装最新内核 " + m.kernel.Version)
			return nil
		}
	}
	candidate, err := downloadKernel(ctx, m.downloads, m.kernelDir(), release, asset, progress)
	if err != nil {
		return err
	}
	candidatePath := filepath.Join(m.kernelDir(), candidate.File)
	keep := false
	defer func() {
		if !keep {
			os.Remove(candidatePath)
		}
	}()
	progress("正在执行新内核自检…")
	if err := probeKernel(ctx, candidatePath, candidate.Version); err != nil {
		return err
	}
	if m.profiles.Active != "" {
		if _, err := os.Stat(m.configPath()); err == nil {
			if err := m.validate(ctx, candidatePath, m.configPath()); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Download and validate before touching the running process or activation record.
	if err := m.refresh(ctx); err != nil {
		return fmt.Errorf("无法保存运行状态，取消更新: %w", err)
	}
	previous := m.state
	if previous.Running {
		progress("正在切换内核，现有连接将短暂中断…")
		if err := m.stop(); err != nil {
			return err
		}
		if err := m.start(ctx, candidatePath); err != nil {
			return m.rollbackKernel(ctx, previous, err)
		}
		if err := m.restoreSession(ctx, previous); err != nil {
			return m.rollbackKernel(ctx, previous, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return m.rollbackKernel(ctx, previous, err)
	}
	data, err := json.Marshal(candidate)
	if err == nil {
		err = writePrivate(filepath.Join(m.dir, "kernel.json"), data)
	}
	if err != nil {
		return m.rollbackKernel(ctx, previous, err)
	}
	oldPath := m.kernelPath()
	m.kernel = candidate
	keep = true
	if oldPath != "" && oldPath != candidatePath {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(&m.logs, "[Clash Go] 旧内核清理失败: %v\n", err)
		}
	}
	fmt.Fprintf(&m.logs, "[Clash Go] 已安装并启用内核 %s\n", candidate.Version)
	progress("内核已更新至 " + candidate.Version)
	return nil
}

func (m *Manager) restoreSession(ctx context.Context, state Snapshot) error {
	if err := m.api.request(ctx, http.MethodPatch, "/configs", map[string]string{"mode": state.Mode}, nil); err != nil {
		return err
	}
	for _, group := range state.Groups {
		if group.Type != "Selector" || group.Now == "" {
			continue
		}
		if err := m.api.request(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group.Name), map[string]string{"name": group.Now}, nil); err != nil {
			return err
		}
	}
	return m.refresh(ctx)
}

func (m *Manager) rollbackKernel(ctx context.Context, previous Snapshot, cause error) error {
	if !previous.Running {
		return cause
	}
	if err := m.stop(); err != nil {
		return errors.Join(cause, fmt.Errorf("停止新内核失败: %w", err))
	}
	// A canceled application is shutting down: retain the old record without restarting.
	if ctx.Err() != nil {
		return cause
	}
	progressErr := m.start(ctx, m.kernelPath())
	if progressErr == nil {
		progressErr = m.restoreSession(ctx, previous)
	}
	if progressErr != nil {
		return errors.Join(cause, fmt.Errorf("已保留旧内核，但恢复运行失败: %w", progressErr))
	}
	return fmt.Errorf("更新失败，已恢复旧内核运行: %w", cause)
}
