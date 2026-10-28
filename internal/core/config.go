package core

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const MaxConfigSize = 8 << 20
const DefaultMixedPort = 7890

// Normalize imports routing data, not listeners or privilege-changing options.
// Configs and their providers must still come from a trusted source.
func Normalize(data []byte, controller, secret string, port int) ([]byte, error) {
	if port < 1024 || port > 65535 {
		return nil, fmt.Errorf("代理端口必须在 1024–65535 之间")
	}
	if len(data) > MaxConfigSize {
		return nil, fmt.Errorf("配置不能超过 8 MiB")
	}
	var source map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&source); err != nil {
		return nil, fmt.Errorf("无效 YAML: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("只支持单个 YAML 文档")
	}
	if source == nil || (source["proxies"] == nil && source["proxy-providers"] == nil && source["rules"] == nil) {
		return nil, fmt.Errorf("需要完整的 mihomo YAML 配置，不支持节点链接或 Base64 订阅")
	}
	out := map[string]any{}
	for _, key := range []string{"proxies", "proxy-groups", "proxy-providers", "rule-providers", "rules", "sub-rules", "hosts", "dns", "mode", "log-level", "ipv6", "unified-delay", "tcp-concurrent", "geodata-mode"} {
		if value, ok := source[key]; ok {
			out[key] = value
		}
	}
	for _, key := range []string{"proxy-providers", "rule-providers"} {
		if out[key] == nil {
			continue
		}
		providers, ok := out[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s 必须为映射", key)
		}
		for name, value := range providers {
			provider, ok := value.(map[string]any)
			if !ok || (provider["type"] != "http" && provider["type"] != "inline") {
				return nil, fmt.Errorf("%s/%s 仅支持 http 或 inline provider", key, name)
			}
			if provider["type"] == "http" {
				delete(provider, "path")
				identity, err := yaml.Marshal(provider)
				if err != nil {
					return nil, fmt.Errorf("无法生成 provider 缓存标识: %w", err)
				}
				id := sha256.Sum256(append([]byte(key+"/"+name+"/"), identity...))
				provider["path"] = fmt.Sprintf("./providers/%x", id[:16])
			} else {
				delete(provider, "path")
			}
		}
	}
	if value, exists := out["dns"]; exists {
		dns, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dns 必须为映射")
		}
		dns["listen"] = ""
	}
	out["mixed-port"] = port
	out["allow-lan"] = false
	out["bind-address"] = "127.0.0.1"
	out["external-controller"] = controller
	out["secret"] = secret
	out["external-controller-cors"] = map[string]any{"allow-origins": []string{}, "allow-private-network": false}
	out["tun"] = map[string]any{"enable": false}
	out["profile"] = map[string]any{"store-selected": true}
	if out["mode"] == nil {
		out["mode"] = "rule"
	}
	if out["log-level"] == nil {
		out["log-level"] = "info"
	}
	return yaml.Marshal(out)
}

func ReadConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigSize+1))
	if err == nil && len(data) > MaxConfigSize {
		err = fmt.Errorf("配置不能超过 8 MiB")
	}
	return data, err
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".clash-go-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
