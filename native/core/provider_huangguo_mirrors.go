package core

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 黄果 AI 镜像自动发现。
//
// 黄果 AI 隔一阵就换域名（2026-11 旧 *.ediayikma.cc 全部迁到 *.adihsppe.cc，huangguoai.com 国内不通）。
// 站点自己有个发布页 /go-home/，列着当前可用的同站镜像。每次成功访问黄果 AI 后，
// 后台去那个站的 /go-home/ 抓一遍（6 小时最多一次），新域名追加到候选列表并存盘，
// 下次启动直接带上——以后再换域名不用跟着发版。

const huangguoAIMirrorRefreshEvery = 6 * time.Hour

var (
	huangguoAIMirrorMu        sync.Mutex
	huangguoAIDiscovered      []string
	huangguoAIDiscoveredAt    time.Time
	huangguoAIMirrorLoadedDir string
	// 发布页里的同站域名：*.adihsppe.cc / huangguoai.{com,io,me,ai} / hgai<数字>.com
	reHuangguoAIMirrorHost = regexp.MustCompile(`https://((?:[a-z0-9-]+\.)?(?:adihsppe\.cc|huangguoai\.(?:com|io|me|ai)|hgai\d*\.com))(?:[/"'\s<]|$)`)
	reHuangguoAIHostName   = regexp.MustCompile(`^hgai\d*\.com$`)
)

// isHuangguoAIHost：这个域名是不是黄果 AI 的站（新旧域名都认）。
func isHuangguoAIHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch host {
	case "huangguoai.com", "huangguoai.me", "huangguoai.ai", "huangguoai.io":
		return true
	}
	return strings.HasSuffix(host, ".adihsppe.cc") || host == "adihsppe.cc" ||
		strings.HasSuffix(host, ".ediayikma.cc") || strings.HasSuffix(host, ".agdkczeyx.cc") ||
		reHuangguoAIHostName.MatchString(host)
}

// discoveredHuangguoAIMirrors：发布页发现的镜像（按发现顺序）。
func discoveredHuangguoAIMirrors() []string {
	huangguoAIMirrorMu.Lock()
	defer huangguoAIMirrorMu.Unlock()
	return append([]string(nil), huangguoAIDiscovered...)
}

// parseHuangguoAIMirrors：从发布页 HTML 里抠出同站镜像（去重，跳过内置列表里已有的）。
func parseHuangguoAIMirrors(rawHTML string) []string {
	builtin := map[string]bool{}
	for _, m := range huangguoAIBaseMirrors {
		builtin[m] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range reHuangguoAIMirrorHost.FindAllStringSubmatch(rawHTML, -1) {
		host := strings.ToLower(m[1])
		// 只收完整镜像 *.adihsppe.cc；huangguoai.me/.ai/.io、hgai*.com 是发布页，接口 404
		if !strings.HasSuffix(host, "."+huangguoAIWildcardDomain) {
			continue
		}
		origin := "https://" + host
		if seen[origin] || builtin[origin] {
			continue
		}
		seen[origin] = true
		out = append(out, origin)
		if len(out) >= 24 {
			break
		}
	}
	return out
}

func huangguoAIMirrorFile(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "huangguoai_mirrors.txt")
}

// loadHuangguoAIMirrors：启动后第一次用到时，把上次存盘的发现结果读回来。
func loadHuangguoAIMirrors(dataDir string) {
	huangguoAIMirrorMu.Lock()
	defer huangguoAIMirrorMu.Unlock()
	if huangguoAIMirrorLoadedDir == dataDir {
		return
	}
	huangguoAIMirrorLoadedDir = dataDir
	path := huangguoAIMirrorFile(dataDir)
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") && isHuangguoAIHost(strings.TrimPrefix(line, "https://")) {
			huangguoAIDiscovered = append(huangguoAIDiscovered, line)
		}
	}
	if info, err := os.Stat(path); err == nil {
		huangguoAIDiscoveredAt = info.ModTime()
	}
}

// maybeRefreshHuangguoAIMirrors：刚成功访问过 origin，后台去它的发布页更新镜像列表。
func (d *Downloader) maybeRefreshHuangguoAIMirrors(origin string) {
	huangguoAIMirrorMu.Lock()
	due := time.Since(huangguoAIDiscoveredAt) >= huangguoAIMirrorRefreshEvery
	if due {
		huangguoAIDiscoveredAt = time.Now() // 先占位，防止并发请求重复去抓
	}
	huangguoAIMirrorMu.Unlock()
	if !due || origin == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/go-home/", nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Referer", origin+"/")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		resp, err := d.doCatalogRequestWithTimeout(req, 12*time.Second)
		if err != nil {
			return
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, providerMaxBodyBytes))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		found := parseHuangguoAIMirrors(string(body))
		if len(found) == 0 {
			return
		}
		huangguoAIMirrorMu.Lock()
		huangguoAIDiscovered = found
		huangguoAIDiscoveredAt = time.Now()
		huangguoAIMirrorMu.Unlock()
		if path := huangguoAIMirrorFile(d.cfg.dataDir); path != "" {
			_ = os.WriteFile(path, []byte(strings.Join(found, "\n")+"\n"), 0o644)
		}
	}()
}
