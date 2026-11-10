package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 黄果 AI 取流（2026-11-09 站点改版后）。
//
// 播放页的 videoInitialData 里 videoSrc / epPlaySrcs 都变成空的，播放器改为
// POST /api/videos/<剧ID>/episodes/<集号>/playback（body "{}"，匿名可用），返回
//   {"data":{"manifest_method":"GET","id":"2833","ep":1,"streams":{"main":"https://…m3u8?auth_key=…","hevc":""},
//            "cdn":{"authKey":"x-cdn-auth","authValue":"…","app":"150","m3u8Enabled":true}}}
// 2026-11-10 实测 m3u8 / crypt.key / ts 不带 x-cdn-auth 也都是 200，所以只用 main，沿用现有 HLS 流程。

var (
	reHuangguoAIPagePath    = regexp.MustCompile(`/video/(\d+)/(?:ep-0*(\d+)/?)?`)
	reHuangguoAIInitialData = regexp.MustCompile(`(?is)<script\b[^>]*id=["']videoInitialData["'][^>]*>(.*?)</script>`)
)

// aiInitialDataTitle：播放页 videoInitialData 里的剧名（不带集数）。
func aiInitialDataTitle(rawHTML string) string {
	m := reHuangguoAIInitialData.FindStringSubmatch(rawHTML)
	if len(m) < 2 {
		return ""
	}
	var data struct {
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(html.UnescapeString(m[1])), &data) != nil {
		return ""
	}
	return strings.TrimSpace(data.Title)
}

// huangguoAIPageEpisode：从播放页地址取 剧ID 和 集号（/video/2833/ = 第 1 集，/video/2833/ep-7/ = 第 7 集）。
func huangguoAIPageEpisode(pageURL string) (id string, ep int, ok bool) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", 0, false
	}
	m := reHuangguoAIPagePath.FindStringSubmatch(u.Path)
	if len(m) < 2 {
		return "", 0, false
	}
	ep = 1
	if m[2] != "" {
		ep, _ = strconv.Atoi(m[2])
	}
	return m[1], ep, ep > 0
}

type huangguoAIPlaybackEnvelope struct {
	Status json.Number `json:"status"`
	Code   json.Number `json:"code"`
	Msg    string      `json:"msg"`
	Data   struct {
		ManifestMethod string      `json:"manifest_method"`
		ID             json.Number `json:"id"`
		Ep             json.Number `json:"ep"`
		Streams        struct {
			Main string `json:"main"`
			Hevc string `json:"hevc"`
		} `json:"streams"`
	} `json:"data"`
}

// parseHuangguoAIPlayback：校验返回的确实是请求的那一集，取 main 播放地址。
func parseHuangguoAIPlayback(body []byte, id string, ep int) (string, error) {
	var env huangguoAIPlaybackEnvelope
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		return "", fmt.Errorf("黄果 AI 取流返回的不是 JSON：%w", err)
	}
	if env.Status.String() != "1" && env.Code.String() != "200" {
		if env.Msg != "" {
			return "", errors.New("黄果 AI 取流失败：" + env.Msg)
		}
		return "", errors.New("黄果 AI 取流失败")
	}
	data := env.Data
	if !strings.EqualFold(data.ManifestMethod, "GET") || data.ID.String() != id || data.Ep.String() != strconv.Itoa(ep) {
		return "", errors.New("黄果 AI 取流返回的剧集与请求不符")
	}
	main := strings.TrimSpace(data.Streams.Main)
	if !strings.HasPrefix(main, "https://") {
		return "", errors.New("黄果 AI 未返回播放地址")
	}
	return main, nil
}

// fetchHuangguoAIPlayback：按候选镜像（上次成功的排最前）依次 POST 取流接口。
// 返回播放地址和该用的 Referer（对应镜像上的播放页）。
func (d *Downloader) fetchHuangguoAIPlayback(ctx context.Context, id string, ep int) (string, string, error) {
	loadHuangguoAIMirrors(d.cfg.dataDir)
	apiPath := fmt.Sprintf("/api/videos/%s/episodes/%d/playback", url.PathEscape(id), ep)
	pagePath := fmt.Sprintf("/video/%s/", url.PathEscape(id))
	if ep > 1 {
		pagePath += fmt.Sprintf("ep-%d/", ep)
	}
	var lastErr error
	tried := 0
	for _, candidate := range d.providerURLCandidates(huangguoAIBaseURL + apiPath) {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		u, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		origin := u.Scheme + "://" + u.Host
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, candidate, strings.NewReader("{}"))
		if err != nil {
			return "", "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", origin+pagePath)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		tried++
		resp, err := d.doCatalogRequestWithTimeout(req, 6*time.Second)
		if err != nil {
			lastErr = err
			var backoff *requestBackoff
			if errors.As(err, &backoff) {
				break
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("黄果 AI 取流接口 HTTP %d", resp.StatusCode)
			if resp.StatusCode == http.StatusNotFound {
				break // 这一集确实没有，换镜像也一样
			}
			continue
		}
		main, err := parseHuangguoAIPlayback(body, id, ep)
		if err != nil {
			return "", "", err // 站点正常回应了，换镜像结果也一样
		}
		d.providerMu.Lock()
		d.providerHosts[sourceHuangguoAI] = origin
		d.providerMu.Unlock()
		return main, origin + pagePath, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的黄果 AI 镜像")
	}
	if tried > 1 {
		return "", "", fmt.Errorf("黄果 AI 取流失败：已尝试 %d 个域名，最后错误：%w", tried, lastErr)
	}
	return "", "", lastErr
}
