package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 红果取流的两个补漏（按 zfdang/hongguo-open-player 的 app-source.js 公开算法用 Go 重写）：
//
//  1. key_seed：个别集的 video_model 带 key_seed，main_url / backup_url / backup_url_list 不是明文，
//     而是 base64( a8 ?? 01 00 ‖ AES-128-CBC 密文 )。key/iv = SHA512(SHA512(seed) ‖ 固定常量) 的前 16 / 次 16 字节，
//     解出后去 PKCS7 填充再去尾部 0。seed = base64 解码 key_seed。
//  2. fallback_api：video_model 里没有内联 video_list，只给一个 fallback_api 地址，
//     GET 它，片源模型在 video_info.data 里（code≠0 表示本集暂无）。

var hongguoSpadeURLConstant, _ = hex.DecodeString("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")

// 解出来的地址只认红果的视频 CDN（同 app-source.js 的 checkedUrl），防止被塞进奇怪的地址
var hongguoVideoCDNDomains = []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"}

// decodeLooseBase64：宽松 base64（标准 / url-safe，有无填充都行），对齐 Node 的 Buffer.from(…, 'base64')。
func decodeLooseBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("-", "+", "_", "/").Replace(value)
	value = strings.TrimRight(value, "=")
	return base64.RawStdEncoding.DecodeString(value)
}

// hongguoKeySeed：video_model.key_seed（没有就是 nil）。
func hongguoKeySeed(model map[string]any) []byte {
	raw := strings.TrimSpace(mapString(model, "key_seed"))
	if raw == "" {
		return nil
	}
	seed, err := decodeLooseBase64(raw)
	if err != nil || len(seed) == 0 {
		return nil
	}
	return seed
}

// decryptHongguoSpadeURL：解 key_seed 包裹的片源地址；不是这种格式或解不出合法地址返回 false。
func decryptHongguoSpadeURL(value string, seed []byte) (string, bool) {
	if value == "" || len(seed) == 0 {
		return "", false
	}
	raw, err := decodeLooseBase64(value)
	if err != nil || len(raw) < 5 || raw[0] != 0xa8 || raw[2] != 0x01 || raw[3] != 0x00 {
		return "", false
	}
	n := (len(raw) - 4) / aes.BlockSize * aes.BlockSize
	if n == 0 {
		return "", false
	}
	h1 := sha512.Sum512(seed)
	h2 := sha512.Sum512(append(h1[:], hongguoSpadeURLConstant...))
	block, err := aes.NewCipher(h2[:16])
	if err != nil {
		return "", false
	}
	plain := make([]byte, n)
	cipher.NewCBCDecrypter(block, h2[16:32]).CryptBlocks(plain, raw[4:4+n])
	if pad := int(plain[len(plain)-1]); pad >= 1 && pad <= aes.BlockSize && pad <= len(plain) {
		plain = plain[:len(plain)-pad]
	}
	for len(plain) > 0 && plain[len(plain)-1] == 0 {
		plain = plain[:len(plain)-1]
	}
	address := strings.TrimSpace(string(plain))
	if !isHongguoVideoCDNURL(address) {
		return "", false
	}
	return address, true
}

func isHongguoVideoCDNURL(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range hongguoVideoCDNDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// hongguoHasInlineList：video_model 里有没有内联的片源列表。
func hongguoHasInlineList(model map[string]any) bool {
	if rows, ok := model["video_list"].(map[string]any); ok {
		return len(rows) > 0
	}
	return len(anyList(model["video_list"])) > 0
}

// hongguoFallbackAPI：fallback_api 可能是字符串、JSON 字符串、数组，或包在对象里。
func hongguoFallbackAPI(value any) string {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "{") {
			var inner map[string]any
			if json.Unmarshal([]byte(v), &inner) == nil {
				return mapString(inner, "fallback_api")
			}
		}
		return v
	case []any:
		if len(v) > 0 {
			s, _ := v[0].(string)
			return strings.TrimSpace(s)
		}
	case map[string]any:
		return mapString(v, "fallback_api")
	}
	return ""
}

// hongguoMediaFromModel：有内联列表直接选；没有就跟 fallback_api 再取一次。
func (downloader *Downloader) hongguoMediaFromModel(ctx context.Context, model map[string]any) (providerMedia, error) {
	if hongguoHasInlineList(model) {
		return selectHongguoAppMedia(model)
	}
	fallback := hongguoFallbackAPI(model["fallback_api"])
	if fallback == "" {
		return selectHongguoAppMedia(model)
	}
	info, err := downloader.fetchHongguoFallbackModel(ctx, fallback)
	if err != nil {
		return providerMedia{}, err
	}
	for _, key := range []string{"video_duration", "duration", "key_seed"} {
		if _, ok := info[key]; !ok && model[key] != nil {
			info[key] = model[key]
		}
	}
	return selectHongguoAppMedia(info)
}

func (downloader *Downloader) fetchHongguoFallbackModel(ctx context.Context, address string) (map[string]any, error) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("红果 fallback_api 地址无效")
	}
	host := strings.ToLower(u.Hostname())
	trusted := false
	for _, domain := range []string{"fqnovel.com", "snssdk.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			trusted = true
		}
	}
	if !trusted {
		return nil, errors.New("红果 fallback_api 地址不受信任")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hongguoAppUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := downloader.doCatalogRequestWithTimeout(req, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("红果 fallback_api 请求失败：%w", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		return nil, fmt.Errorf("红果 fallback_api HTTP %d", resp.StatusCode)
	}
	var outer map[string]any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&outer); err != nil {
		return nil, errors.New("红果 fallback_api 返回格式异常")
	}
	videoInfo := nestedMap(outer, "video_info")
	for _, code := range []string{mapString(outer, "code", "Code"), mapString(videoInfo, "code", "Code")} {
		if code != "" && code != "0" {
			return nil, fmt.Errorf("红果暂未提供本集播放信息（fallback_api code=%s）", code)
		}
	}
	data := nestedMap(videoInfo, "data")
	if len(data) == 0 {
		return nil, errors.New("红果 fallback_api 未返回片源")
	}
	return data, nil
}
