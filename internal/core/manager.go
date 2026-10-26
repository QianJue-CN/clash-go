package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Group struct {
	Name string
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type Snapshot struct {
	Port                int
	KernelVersion       string
	KernelReady         bool
	Running, HasConfig  bool
	Version, Mode, Logs string
	Started             time.Time
	Upload, Download    int64
	Connections         int
	Groups              []Group
}

type logBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 64 << 10
	n := len(p)
	if n >= limit {
		b.data = append(b.data[:0], p[n-limit:]...)
	} else {
		if len(b.data)+n > limit {
			b.data = b.data[len(b.data)+n-limit:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(string(b.data), "�")
}

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error // Read only after done is closed.
}

type Manager struct {
	mu        sync.Mutex
	dir       string
	proc      *process
	api       *apiClient
	logs      logBuffer
	state     Snapshot
	settings  Settings
	kernel    installedKernel
	downloads *http.Client
}

func NewManager(dir string) (*Manager, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(absolute, 0700); err != nil {
		return nil, err
	}
	m := &Manager{dir: absolute, settings: Settings{Port: DefaultMixedPort}, downloads: kernelHTTP()}
	data, err := os.ReadFile(filepath.Join(absolute, "settings.json"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &m.settings); err != nil {
			return nil, fmt.Errorf("读取设置失败: %w", err)
		}
	}
	if err := m.settings.validate(); err != nil {
		return nil, err
	}
	if err := m.loadKernel(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) ConfigPath() string  { return filepath.Join(m.dir, "config.yaml") }
func (m *Manager) runtimePath() string { return filepath.Join(m.dir, "runtime.yaml") }

func coreCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath(strings.TrimSpace(binary))
	if err != nil {
		return nil, fmt.Errorf("托管内核不可用，请在设置中重新下载内核: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if ctx == nil {
		cmd = exec.Command(path, args...)
	} else {
		cmd = exec.CommandContext(ctx, path, args...)
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "CLASH_") || strings.HasPrefix(key, "MIHOMO_") || key == "SKIP_SAFE_PATH_CHECK" || key == "SAFE_PATHS" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

func (m *Manager) validate(ctx context.Context, binary, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := coreCommand(ctx, binary, "-t", "-d", m.dir, "-f", path)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = &m.logs, &m.logs
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mihomo 配置校验失败（详见日志）: %w", err)
	}
	return nil
}

func (m *Manager) Import(ctx context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyKernel(ctx); err != nil {
		return err
	}
	m.reap()
	if m.proc != nil {
		return fmt.Errorf("请先停止内核再替换配置")
	}
	normalized, err := Normalize(data, "", "", m.settings.Port)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".validate-*.yaml")
	if err != nil {
		return err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	defer os.Remove(path)
	if err := writePrivate(path, normalized); err != nil {
		return err
	}
	if err := m.validate(ctx, m.kernelPath(), path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writePrivate(m.ConfigPath(), normalized)
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyKernel(ctx); err != nil {
		return err
	}
	return m.start(ctx, m.kernelPath())
}

func (m *Manager) start(ctx context.Context, binary string) error {
	m.reap()
	if m.proc != nil {
		return fmt.Errorf("内核已在运行")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := ReadConfig(m.ConfigPath())
	if err != nil {
		return fmt.Errorf("请先导入配置: %w", err)
	}
	// Reserve a free loopback port; if it is stolen before exec, readiness fails closed.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	defer listener.Close()
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(secretBytes); err != nil {
		return err
	}
	secret := hex.EncodeToString(secretBytes)
	data, err = Normalize(data, address, secret, m.settings.Port)
	if err != nil {
		return err
	}
	if err = writePrivate(m.runtimePath(), data); err != nil {
		return err
	}
	defer func() {
		if m.proc == nil {
			os.Remove(m.runtimePath())
		}
	}()
	if err = m.validate(ctx, binary, m.runtimePath()); err != nil {
		return err
	}
	// Refuse an occupied proxy port rather than reporting a running but unusable core.
	proxyListener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", m.settings.Port))
	if err != nil {
		return fmt.Errorf("本地代理端口 %d 已被占用，请在设置中更换端口: %w", m.settings.Port, err)
	}
	proxyListener.Close()
	cmd, err := coreCommand(nil, binary, "-d", m.dir, "-f", m.runtimePath())
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = &m.logs, &m.logs
	if err = ctx.Err(); err != nil {
		return err
	}
	listener.Close()
	if err = cmd.Start(); err != nil {
		return err
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	m.proc = p
	m.api = &apiClient{base: "http://" + address, secret: secret, http: directHTTP(3 * time.Second)}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		var version struct {
			Version string `json:"version"`
		}
		if err := m.api.request(readyCtx, http.MethodGet, "/version", nil, &version); err == nil && version.Version != "" {
			select {
			case <-p.done:
				m.reap()
				return fmt.Errorf("内核启动后退出，请查看日志")
			default:
			}
			m.state = Snapshot{Running: true, Version: version.Version, Started: time.Now()}
			return nil
		}
		select {
		case <-p.done:
			m.reap()
			return fmt.Errorf("内核提前退出，请查看日志")
		case <-readyCtx.Done():
			m.stop()
			return fmt.Errorf("等待内核就绪失败: %w", readyCtx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) reap() {
	if m.proc == nil {
		return
	}
	select {
	case <-m.proc.done:
		if m.proc.err != nil {
			fmt.Fprintf(&m.logs, "\n[Clash Go] 内核退出: %v\n", m.proc.err)
		}
		m.proc = nil
		m.api.http.CloseIdleConnections()
		m.api = nil
		m.state = Snapshot{}
		os.Remove(m.runtimePath())
	default:
	}
}

func (m *Manager) stop() error {
	m.reap()
	if m.proc == nil {
		return nil
	}
	p := m.proc
	// Windows does not support os.Interrupt for child processes; kill is the fallback.
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		if err := p.cmd.Process.Kill(); err != nil && err != os.ErrProcessDone {
			return err
		}
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		if err := p.cmd.Process.Kill(); err != nil && err != os.ErrProcessDone {
			return err
		}
		<-p.done
	}
	m.reap()
	return nil
}
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stop()
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	s := m.state
	s.Port = m.settings.Port
	s.KernelVersion = m.kernel.Version
	s.KernelReady = m.kernel.File != ""
	_, err := os.Stat(m.ConfigPath())
	s.HasConfig = err == nil
	s.Logs = m.logs.String()
	return s
}

func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refresh(ctx)
}

func (m *Manager) refresh(ctx context.Context) error {
	m.reap()
	if m.api == nil {
		return nil
	}
	var configs struct {
		Mode string `json:"mode"`
	}
	if err := m.api.request(ctx, http.MethodGet, "/configs", nil, &configs); err != nil {
		return err
	}
	var proxies struct {
		Proxies map[string]Group `json:"proxies"`
	}
	if err := m.api.request(ctx, http.MethodGet, "/proxies", nil, &proxies); err != nil {
		return err
	}
	var connections struct {
		Upload      int64 `json:"uploadTotal"`
		Download    int64 `json:"downloadTotal"`
		Connections []any `json:"connections"`
	}
	if err := m.api.request(ctx, http.MethodGet, "/connections", nil, &connections); err != nil {
		return err
	}
	groups := []Group{}
	for name, group := range proxies.Proxies {
		if len(group.All) > 0 {
			group.Name = name
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	m.state.Mode, m.state.Groups = configs.Mode, groups
	m.state.Upload, m.state.Download, m.state.Connections = connections.Upload, connections.Download, len(connections.Connections)
	return nil
}

func (m *Manager) SetMode(ctx context.Context, mode string) error {
	if mode != "rule" && mode != "global" && mode != "direct" {
		return fmt.Errorf("无效代理模式")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	if m.api == nil {
		return fmt.Errorf("内核未运行")
	}
	return m.api.request(ctx, http.MethodPatch, "/configs", map[string]string{"mode": mode}, nil)
}
func (m *Manager) Select(ctx context.Context, group, proxy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	if m.api == nil {
		return fmt.Errorf("内核未运行")
	}
	return m.api.request(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group), map[string]string{"name": proxy}, nil)
}

type Settings struct {
	Port int `json:"port"`
}

func (s Settings) validate() error {
	if s.Port < 1024 || s.Port > 65535 {
		return fmt.Errorf("代理端口必须在 1024–65535 之间")
	}
	return nil
}
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}
func (m *Manager) SaveSettings(settings Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap()
	if m.proc != nil {
		return fmt.Errorf("请先停止内核再修改设置")
	}
	if err := settings.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(m.dir, "settings.json"), data); err != nil {
		return err
	}
	m.settings = settings
	return nil
}
