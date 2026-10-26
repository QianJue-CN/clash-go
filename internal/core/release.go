package core

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const latestReleaseURL = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"
const maxArchiveSize = 128 << 20
const maxBinarySize = 256 << 20

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type kernelRelease struct {
	Version    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

func versionParts(version string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if !strings.HasPrefix(version, "v") || len(parts) != 3 {
		return result, fmt.Errorf("无效的稳定版版本号")
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return result, fmt.Errorf("无效的稳定版版本号")
		}
		result[i] = n
	}
	return result, nil
}

func assetName(goos, arch, version string) (string, error) {
	if _, err := versionParts(version); err != nil {
		return "", err
	}
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (arch != "amd64" && arch != "arm64") {
		return "", fmt.Errorf("暂不支持自动安装 %s/%s 内核", goos, arch)
	}
	variant := arch
	if arch == "amd64" {
		variant += "-compatible"
	}
	extension := ".gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return "mihomo-" + goos + "-" + variant + "-" + version + extension, nil
}

func officialURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func kernelHTTP() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Downloads can use an operator-configured HTTPS_PROXY; the local API never does.
	return &http.Client{Transport: transport, Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !officialURL(req.URL.String()) {
			return fmt.Errorf("拒绝非官方或不安全的下载重定向")
		}
		return nil
	}}
}

func releaseGET(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	if !officialURL(address) {
		return nil, fmt.Errorf("拒绝非官方内核下载地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ClashGo/0.1")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("连接 GitHub 失败，请检查网络或 HTTPS_PROXY 设置")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub 返回 HTTP %d（403/429 可能是请求限流），请稍后重试", resp.StatusCode)
	}
	return resp, nil
}

func latestKernel(ctx context.Context, client *http.Client, goos, arch string) (kernelRelease, releaseAsset, error) {
	var release kernelRelease
	var asset releaseAsset
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := releaseGET(ctx, client, latestReleaseURL)
	if err != nil {
		return release, asset, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return release, asset, err
	}
	if len(data) > 2<<20 {
		return release, asset, fmt.Errorf("版本信息过大")
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return release, asset, fmt.Errorf("无法解析 GitHub 版本信息: %w", err)
	}
	if release.Draft || release.Prerelease {
		return release, asset, fmt.Errorf("拒绝非稳定版内核")
	}
	name, err := assetName(goos, arch, release.Version)
	if err != nil {
		return release, asset, err
	}
	for _, item := range release.Assets {
		if item.Name != name {
			continue
		}
		expected := "https://github.com/MetaCubeX/mihomo/releases/download/" + release.Version + "/" + name
		if item.URL != expected || item.Size <= 0 || item.Size > maxArchiveSize || !strings.HasPrefix(item.Digest, "sha256:") {
			return release, asset, fmt.Errorf("发行资源的地址、大小或 SHA-256 校验信息无效")
		}
		if _, err := parseHash(strings.TrimPrefix(item.Digest, "sha256:")); err != nil {
			return release, asset, err
		}
		return release, item, nil
	}
	return release, asset, fmt.Errorf("最新发行版未提供 %s", name)
}

func parseHash(value string) ([]byte, error) {
	data, err := hex.DecodeString(value)
	if err != nil || len(data) != sha256.Size {
		return nil, fmt.Errorf("缺少有效的 SHA-256 校验值，拒绝执行下载内容")
	}
	return data, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, maxBinarySize+1))
	if err != nil {
		return "", err
	}
	if n > maxBinarySize {
		return "", fmt.Errorf("内核文件过大")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func downloadKernel(ctx context.Context, client *http.Client, dir string, release kernelRelease, asset releaseAsset, progress func(string)) (installedKernel, error) {
	var kernel installedKernel
	resp, err := releaseGET(ctx, client, asset.URL)
	if err != nil {
		return kernel, err
	}
	defer resp.Body.Close()
	archive, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		return kernel, err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var total int64
	lastPercent := -1
	reader := io.LimitReader(resp.Body, asset.Size+1)
	for {
		if err := ctx.Err(); err != nil {
			return kernel, err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := archive.Write(buffer[:n]); err != nil {
				return kernel, err
			}
			hash.Write(buffer[:n])
			total += int64(n)
			percent := int(total * 100 / asset.Size)
			if percent != lastPercent {
				progress(fmt.Sprintf("正在下载 %s · %d%%", release.Version, percent))
				lastPercent = percent
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return kernel, fmt.Errorf("内核下载中断: %w", readErr)
		}
	}
	if total != asset.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != asset.Digest {
		return kernel, fmt.Errorf("内核下载大小或 SHA-256 校验不匹配，已拒绝安装")
	}
	if err := archive.Close(); err != nil {
		return kernel, err
	}
	progress("校验通过，正在解压内核…")
	extension := ""
	if strings.HasSuffix(asset.Name, ".zip") {
		extension = ".exe"
	}
	binary, err := os.CreateTemp(dir, "mihomo-*"+extension)
	if err != nil {
		return kernel, err
	}
	keep := false
	defer func() {
		binary.Close()
		if !keep {
			os.Remove(binary.Name())
		}
	}()
	if err := unpackKernel(archive.Name(), asset.Name, release.Version, binary); err != nil {
		return kernel, err
	}
	if err := binary.Sync(); err != nil {
		return kernel, err
	}
	if err := binary.Chmod(0700); err != nil {
		return kernel, err
	}
	if err := binary.Close(); err != nil {
		return kernel, err
	}
	digest, err := hashFile(binary.Name())
	if err != nil {
		return kernel, err
	}
	if err := ctx.Err(); err != nil {
		return kernel, err
	}
	keep = true
	return installedKernel{Version: release.Version, File: filepath.Base(binary.Name()), SHA256: digest}, nil
}

// Archive filenames are never used as output paths, including on Windows.
func unpackKernel(path, name, version string, target io.Writer) error {
	var reader io.ReadCloser
	if strings.HasSuffix(name, ".zip") {
		archive, err := zip.OpenReader(path)
		if err != nil {
			return err
		}
		defer archive.Close()
		expected := strings.TrimSuffix(name, "-"+version+".zip") + ".exe"
		if len(archive.File) != 1 || archive.File[0].Name != expected || !archive.File[0].Mode().IsRegular() || archive.File[0].UncompressedSize64 > maxBinarySize {
			return fmt.Errorf("ZIP 中未找到唯一且安全的 mihomo 可执行文件")
		}
		reader, err = archive.File[0].Open()
		if err != nil {
			return err
		}
	} else if strings.HasSuffix(name, ".gz") {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		reader, err = gzip.NewReader(file)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("不支持的内核压缩格式")
	}
	defer reader.Close()
	n, err := io.Copy(target, io.LimitReader(reader, maxBinarySize+1))
	if err != nil {
		return fmt.Errorf("内核解压失败: %w", err)
	}
	if n == 0 || n > maxBinarySize {
		return fmt.Errorf("解压后的内核大小无效")
	}
	return nil
}
