package core

// 红果「V5 播放签名」方言（7.4.5.32 / medusa 第 21 字段 = 312）。
//
// 上游 2026-11-23 起把 field 25 方言（provider_hongguo_current.go 里的 HGCurrentPlayer.sign）
// 的整体请求判为无效：POST 任何播放端点都只回 HTTP 200 + 0 字节（软拒绝）。
// 现行取流形态是：
//
//	POST https://<host>/novel/player/multi_video_model/preload/v1  （gzip 请求体 + V5 三头签名）
//	GET  https://<host>/reading/bookapi/video_tab/multi_video_model/v1/ （无请求体）
//
// 签名头只有 x-khronos / x-helios / x-medusa（+ 有请求体时的 Content-Type 与 x-ss-stub）；
// 不再有 x-argus / x-gorgon / x-ladon。
//
// 逐行移植自 iDuanJu STV/Providers/Hongguo/：HGCurrentPlayer.signPlaybackV5 / hashF13V5 /
// medusaV5 与 HGPlayerSign 的 expandWords / mixRounds / sumMD5 / xmxor / keyHash。
// 常量表复用 hongguoCurrentProfile（material/header/boxes/factorIV/factorTable/padding）。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hongguoPlaybackV5License / Aid：x-helios 与 protobuf 里固定的红果授权号与应用号。
const (
	hongguoPlaybackV5License = "646637794"
	hongguoPlaybackV5Aid     = "8662"
	// hongguoPlaybackV5UA：preload/v1 走的 okhttp 客户端标识。
	hongguoPlaybackV5UA = "okhttp/4.12.0"
	// hongguoPlaybackV5TabUA：video_tab 兜底通道用的 Cronet 标识。
	hongguoPlaybackV5TabUA = "com.phoenix.read/73932 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
)

type hongguoPlaybackV5Signed struct {
	Query   string
	Headers map[string]string
}

var (
	hongguoPlaybackV5ProcessOnce  sync.Once
	hongguoPlaybackV5ProcessNonce string
	hongguoPlaybackV5ProcessStart int64
)

// hongguoPlaybackV5Process 返回「进程级」nonce 与启动时间，medusa 里作为设备进程标识复用。
func hongguoPlaybackV5Process() (string, int64) {
	hongguoPlaybackV5ProcessOnce.Do(func() {
		raw := make([]byte, 18)
		_, _ = rand.Read(raw)
		hongguoPlaybackV5ProcessNonce = base64.RawURLEncoding.EncodeToString(raw)
		hongguoPlaybackV5ProcessStart = time.Now().UnixMilli()
	})
	return hongguoPlaybackV5ProcessNonce, hongguoPlaybackV5ProcessStart
}

// hongguoPlaybackV5Sign 生成 V5 播放签名。pairs 里不要带 _rticket（会被替换）。
// includeBodyHeaders 为 true 时额外返回 Content-Type 与 x-ss-stub（有请求体时用）。
// field21 为 nil 表示整条 field 21 省略。
func hongguoPlaybackV5Sign(pairs [][2]string, nowMillis int64, body []byte, includeBodyHeaders bool, field21 *int) (hongguoPlaybackV5Signed, error) {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return hongguoPlaybackV5Signed{}, err
	}
	nonce, started := hongguoPlaybackV5Process()
	return hongguoPlaybackV5SignWith(pairs, nowMillis, body, includeBodyHeaders, field21, entropy, nonce, started)
}

// hongguoPlaybackV5SignWith 是固定熵版本，供离线回归向量使用。
func hongguoPlaybackV5SignWith(pairs [][2]string, nowMillis int64, body []byte, includeBodyHeaders bool, field21 *int, entropy []byte, processNonce string, processStarted int64) (hongguoPlaybackV5Signed, error) {
	var signed hongguoPlaybackV5Signed
	if nowMillis <= 1000 || len(entropy) < 16 {
		return signed, errors.New("红果 V5 播放签名参数无效")
	}
	ts := uint32(nowMillis / 1000)
	digest := md5Bytes(body)
	tsBytes := le32(ts)

	base := make([][2]string, 0, len(pairs)+1)
	for _, p := range pairs {
		if p[0] != "_rticket" {
			base = append(base, p)
		}
	}

	msgRand := binary.LittleEndian.Uint32(entropy[0:4])
	nonce32 := binary.LittleEndian.Uint32(entropy[8:12])
	packedRand := binary.LittleEndian.Uint32(entropy[12:16])
	heliosRandom := binary.LittleEndian.Uint32(entropy[4:8])

	// Swift 侧对 _rticket 做 0..1023 的递增搜索，命中 hashF13 的第十二个分支才签。
	for offset := int64(0); offset < 1024; offset++ {
		ticket := nowMillis + offset
		next := make([][2]string, 0, len(base)+1)
		next = append(next, base...)
		next = append(next, [2]string{"_rticket", strconv.FormatInt(ticket, 10)})
		query := hongguoCurrentEncodeQuery(next)
		qhash := hongguoSM3([]byte(query))
		if hongguoPlaybackV5LowBranch(qhash[:], digest[:], tsBytes[:]) != 12 {
			continue
		}
		values := make(map[string]string, len(next))
		for _, p := range next {
			values[p[0]] = p[1]
		}
		medusa, err := hongguoPlaybackV5Medusa(qhash[:], digest[:], ts, values, msgRand, nonce32, packedRand, processNonce, processStarted, field21)
		if err != nil {
			return signed, err
		}
		headers := map[string]string{
			"x-khronos": strconv.FormatUint(uint64(ts), 10),
			"x-helios":  hongguoCurrentHelios(uint64(ts), hongguoPlaybackV5License, hongguoPlaybackV5Aid, heliosRandom),
			"x-medusa":  medusa,
		}
		if includeBodyHeaders {
			headers["Content-Type"] = "application/json; charset=utf-8"
			headers["x-ss-stub"] = strings.ToUpper(hex.EncodeToString(digest[:]))
		}
		return hongguoPlaybackV5Signed{Query: query, Headers: headers}, nil
	}
	return signed, errors.New("红果 V5 播放签名分支搜索失败")
}

// hongguoPlaybackV5LowBranch —— getIV 三级串联后的低四位，必须等于 12。
func hongguoPlaybackV5LowBranch(qhash, digest, tsBytes []byte) uint32 {
	iv := hongguoGetIV(hongguoGetIV(hongguoGetIV(0x20230928, qhash), digest), tsBytes)
	return iv & 15
}

// hongguoPlaybackV5HashF13 —— field 13 的 32 字节摘要（factor 表 + 32 位轮函数 + sumMD5 校验）。
func hongguoPlaybackV5HashF13(qhash, digest []byte, ts uint32) ([]byte, error) {
	tsBytes := le32(ts)
	if hongguoPlaybackV5LowBranch(qhash, digest, tsBytes[:]) != 12 {
		return nil, errors.New("红果 V5 播放签名参数无效")
	}
	const ivV1 uint32 = 0x9d4a97c2
	count := ivV1 + ts

	table := make([]uint32, 64)
	for i := 0; i < 64; i++ {
		table[i] = rotr32(hongguoCurrentFactorTable[i], (count+1)&31)
	}
	shift := (count + 2) & 7
	pad := make([]byte, 4)
	for i := 0; i < 4; i++ {
		v := uint16(hongguoCurrentPadding[i])
		combined := v | v<<8
		pad[i] = byte((combined >> shift) & 255)
	}

	data := make([]byte, 0, 64)
	data = append(data, qhash...)
	data = append(data, digest...)
	data = append(data, tsBytes[:]...)
	data = append(data, pad...)
	data = append(data, 0, 0, 0, 0, 0, 0, 1, 0xa0)

	words := hongguoPlaybackV5ExpandWords(data)
	ini := make([]uint32, 8)
	for i := 0; i < 8; i++ {
		ini[i] = rotr32(hongguoCurrentFactorIV[i], count&31)
	}
	rounds := []uint32{96, 1, 3, 4, 0, 6, 5, 2, 0, 7, 7}
	state := hongguoPlaybackV5MixRounds(ini, rounds, words, table, ivV1)

	ret := make([]byte, 32)
	for i := 0; i < 8; i++ {
		be := hongguoCurrentBE32(state[i] + ini[i])
		copy(ret[i*4:], be[:])
	}
	folded := make([]byte, 16)
	for i := 0; i < 16; i++ {
		folded[i] = ret[i] ^ ret[i+16]
	}
	check := hongguoPlaybackV5SumMD5(folded) | 0x20
	tail := le32(check)
	return append(folded, tail[:]...), nil
}

// hongguoPlaybackV5Medusa —— x-medusa 头：protobuf 消息 -> xmxor -> 位平面置换 -> 自定义 AES。
func hongguoPlaybackV5Medusa(qhash, digest []byte, ts uint32, values map[string]string, msgRand, nonce, packedRand uint32, processNonce string, processStarted int64, field21 *int) (string, error) {
	f13, err := hongguoPlaybackV5HashF13(qhash, digest, ts)
	if err != nil {
		return "", err
	}
	n := func(tag uint32, v int64) []byte {
		return hongguoProto([]hongguoProtoField{hongguoProtoSint(tag, v)})
	}
	s := func(tag uint32, v string) []byte {
		return hongguoProto([]hongguoProtoField{hongguoProtoString(tag, v)})
	}
	b := func(tag uint32, v []byte) []byte {
		return hongguoProto([]hongguoProtoField{hongguoProtoBytes(tag, v)})
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	version := values["version_name"]
	deviceID := values["device_id"]
	osVersion := values["os_version"]
	deviceType := values["device_type"]
	channel := values["channel"]

	deviceInfo := cat(
		n(1, 1), n(2, 2), s(3, hongguoPlaybackV5Aid), s(4, deviceID),
		s(5, processNonce), s(6, "!noperm!"), s(12, "Asia/Shanghai,8"), s(13, "zh_CN"),
		s(22, osVersion), n(25, processStarted), s(30, deviceType), s(31, "HONOR"),
		s(32, deviceType), s(34, "HONOR"),
	)
	env := cat(s(6, "v04.10.03.04-bugfix"), s(8, channel), b(12, deviceInfo), s(14, version))

	flags := make([]byte, 8)
	msg := cat(
		b(1, hongguoCurrentHeader),
		n(2, 5),
		n(3, int64(msgRand)),
		s(4, hongguoPlaybackV5Aid),
		s(5, deviceID),
		s(6, hongguoPlaybackV5License),
		s(7, version),
		s(8, "v04.10.03-ml-android"),
		n(9, 0x040a0300),
		b(10, flags),
		n(12, int64(ts)),
		b(13, f13),
		b(14, qhash[:6]),
		b(15, cat(n(1, 1), n(2, 1), n(3, 1), n(5, 1))),
		n(17, int64(ts)),
		s(20, "none"),
	)
	if field21 != nil {
		msg = append(msg, n(21, int64(*field21))...)
	}
	msg = append(msg, b(23, env)...)
	msg = append(msg, s(24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`)...)

	hash, seed := hongguoPlaybackV5KeyHash(hongguoCurrentMaterial, nonce)
	xored := hongguoPlaybackV5Xmxor(msg, hash)
	tr := make([]byte, 0, len(flags)+len(xored))
	tr = append(tr, flags...)
	tr = append(tr, xored...)
	// Swift 里是 Array((flags + xmxor(msg)).reversed())，再逐字节异或 seed[(~i) & 3]。
	for i, j := 0, len(tr)-1; i < j; i, j = i+1, j-1 {
		tr[i], tr[j] = tr[j], tr[i]
	}
	for i := range tr {
		tr[i] ^= seed[(3-i)&3]
	}

	check := uint32(0x18000001) | (uint32(qhash[0]&63) << 14) | (uint32(f13[0]&63) << 8)
	packed := make([]byte, 0, len(tr)+12)
	packed = append(packed, 0xca)
	pr := le32(packedRand)
	packed = append(packed, pr[:]...)
	cr := le32(check)
	packed = append(packed, cr[:]...)
	packed = append(packed, tr...)
	packed = append(packed, byte(nonce>>16), byte(nonce>>24))
	if len(packed) < 248 {
		return "", errors.New("红果 V5 播放签名参数无效")
	}

	lane := [8]byte{6, 5, 3, 4, 2, 7, 1, 0}
	bitOrder := [8]byte{1, 7, 3, 6, 2, 0, 4, 5}
	plain := make([]byte, 31)
	for i := 0; i < 248; i++ {
		plain[i/8] |= ((packed[i] >> lane[i%8]) & 1) << bitOrder[i%8]
	}

	matKey := md5Bytes(hongguoCurrentMaterial[0:16])
	matIV := md5Bytes(hongguoCurrentMaterial[16:32])
	channelBytes, err := hongguoCurrentTransform(plain, matKey[:], matIV[:], int(ts&3))
	if err != nil {
		return "", err
	}
	if len(channelBytes) < 32 {
		return "", errors.New("红果 V5 播放签名参数无效")
	}

	body := append([]byte(nil), packed...)
	for i := 0; i < 248; i++ {
		body[i] = (body[i] &^ (1 << lane[i%8])) | (((channelBytes[i/8] >> bitOrder[i%8]) & 1) << lane[i%8])
	}

	version5 := le32(5)
	hdr := make([]byte, 0, 20)
	hdr = append(hdr, version5[:]...)
	hdr = append(hdr, hongguoCurrentHeader...)
	mask := le32(ts)
	for i := range hdr {
		hdr[i] ^= mask[i&3]
	}

	out := make([]byte, 0, len(hdr)+5+len(body))
	out = append(out, hdr...)
	out = append(out, byte(nonce), byte(nonce>>8), 0, 1, channelBytes[31])
	out = append(out, body...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// ---------------- HGPlayerSign 帮助函数 ----------------

func hongguoPlaybackV5Rotl32(v, c uint32) uint32 {
	n := c & 31
	if n == 0 {
		return v
	}
	return bits.RotateLeft32(v, int(n))
}

func hongguoPlaybackV5ExpandWords(data []byte) []uint32 {
	words := make([]uint32, 0, 128)
	for off := 0; off+4 <= len(data); off += 4 {
		words = append(words, binary.BigEndian.Uint32(data[off:]))
	}
	first := words[0]
	for i := 0; i < 112; i++ {
		next, far := words[i+1], words[i+14]
		r1 := hongguoPlaybackV5Rotl32(next, 14) ^ hongguoPlaybackV5Rotl32(next, 25) ^ (next >> 3)
		r2 := hongguoPlaybackV5Rotl32(far, 13) ^ hongguoPlaybackV5Rotl32(far, 15) ^ (far >> 10)
		words = append(words, first+words[i+9]+r1+r2)
		first = next
	}
	return words
}

func hongguoPlaybackV5MixRounds(state, rounds, words, table []uint32, ivV1 uint32) []uint32 {
	s := append([]uint32(nil), state...)
	at := func(k int) int { return int(rounds[k]) }
	for i := uint32(0); i < rounds[0]; i++ {
		base := ivV1 + i
		w := words[base&127]
		n1 := ((s[at(3)] ^ s[at(4)]) & s[at(1)]) ^ s[at(3)]
		n2 := hongguoPlaybackV5Rotl32(s[at(1)], 26) ^ hongguoPlaybackV5Rotl32(s[at(1)], 21) ^ hongguoPlaybackV5Rotl32(s[at(1)], 7)
		n4 := w + n1 + n2 + table[base&63] + s[at(5)]
		n5 := hongguoPlaybackV5Rotl32(s[at(2)], 30) ^ hongguoPlaybackV5Rotl32(s[at(2)], 19) ^ hongguoPlaybackV5Rotl32(s[at(2)], 10)
		n6 := (s[at(2)] & s[at(6)]) | ((s[at(2)] | s[at(6)]) & s[at(7)])
		n7 := n5 + n6
		old := s[at(9)]
		last := s[len(s)-1]
		copy(s[1:], s[:len(s)-1])
		s[0] = last
		s[at(10)] = n7 + n4
		s[at(8)] = old + n4
	}
	return s
}

func hongguoPlaybackV5SumMD5(data []byte) uint32 {
	check := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		var temp uint32
		if i&1 == 0 {
			temp = (check >> 3) ^ check
			check = uint32(data[i]) ^ (check << 7)
		} else {
			temp = (check >> 5) ^ check
			check = ^(uint32(data[i]) | (check << 11))
		}
		check ^= temp
	}
	return (check | 4) ^ 0x01000000
}

func hongguoPlaybackV5Xmxor(data, key []byte) []byte {
	n := len(data)
	if n < 4 || len(key) < 32 {
		return nil
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		at := (i * 4) & 28
		d0, d1 := key[at], key[at+1]
		swapped := (data[i] << 4) | (data[i] >> 4)
		v := byte(uint32(swapped) + uint32(d0))
		v = ^v ^ d1
		v = (v << 3) | (v >> 5)
		v += d1
		v ^= d0
		out[n-i-1] = ^v
	}
	last := out[n-1] ^ out[n-2]
	first := out[0]
	out[0] = (^last) + first
	out[1] = (out[0] ^ out[n-1] ^ 254) + out[1]
	shifted := (out[1] << 3) | (out[1] >> 5)
	fix2 := (last - first) ^ (shifted ^ 2)
	out[2] += fix2
	for i := 0; i < n-4; i++ {
		sh := (out[i+2] << 3) | (out[i+2] >> 5)
		temp := sh ^ out[i+1] ^ byte(i+3)
		out[i+3] = (^temp) + out[i+3]
	}
	out[n-1] ^= out[n-2]
	var sum uint32
	for _, item := range out[1:] {
		sum += uint32(item)
	}
	out[0] = (out[0] ^ out[1]) + byte(sum)
	return out
}

func hongguoPlaybackV5KeyHash(signKey []byte, random uint32) ([]byte, []byte) {
	input := make([]byte, 0, len(signKey)*2+4)
	input = append(input, signKey...)
	le := le32(random)
	input = append(input, le[:]...)
	input = append(input, signKey...)
	hash := hongguoSM3(input)

	d1 := (random >> 16) & 255
	d2 := (d1 << 11) | (random >> 24)
	d2 ^= d1 >> 5
	d2 ^= d1
	d2 = ^d2
	seed := le32(d2)
	return hash[:], seed[:]
}

// ---------------- 取流：preload/v1 -> video_tab/v1 ----------------

// hongguoPlaybackV5Pairs —— appQuery + 设备三元组（iid / cdid / device_id）。
func hongguoPlaybackV5Pairs(cfg hongguoProtocol, id hongguoPlayerIdentity) [][2]string {
	pairs := make([][2]string, 0, len(cfg.AppQuery)+3)
	pairs = append(pairs, cfg.AppQuery...)
	pairs = append(pairs,
		[2]string{"iid", id.InstallID},
		[2]string{"cdid", id.CDID},
		[2]string{"device_id", id.DeviceID},
	)
	return pairs
}

var (
	hongguoPlaybackV5HostMu      sync.Mutex
	hongguoPlaybackV5HostPrefer  string
	hongguoPlaybackV5IdMaxLength = 30
)

// hongguoPlaybackV5OrderedHosts —— 优先上次成功的 host，其余按配置顺序。
func hongguoPlaybackV5OrderedHosts(hosts []string) []string {
	hongguoPlaybackV5HostMu.Lock()
	preferred := hongguoPlaybackV5HostPrefer
	hongguoPlaybackV5HostMu.Unlock()
	if preferred == "" {
		return hosts
	}
	found := false
	for _, h := range hosts {
		if h == preferred {
			found = true
			break
		}
	}
	if !found {
		return hosts
	}
	ordered := make([]string, 0, len(hosts))
	ordered = append(ordered, preferred)
	for _, h := range hosts {
		if h != preferred {
			ordered = append(ordered, h)
		}
	}
	return ordered
}

func hongguoPlaybackV5RememberHost(host string) {
	hongguoPlaybackV5HostMu.Lock()
	hongguoPlaybackV5HostPrefer = host
	hongguoPlaybackV5HostMu.Unlock()
}

// resolveHongguoPreloadV5 —— 主通道：POST multi_video_model/preload/v1（gzip 请求体）。
func (downloader *Downloader) resolveHongguoPreloadV5(ctx context.Context, videoID string, identity hongguoPlayerIdentity) (providerMedia, error) {
	cfg := hongguoProtocolCurrent()
	raw, err := json.Marshal(map[string]any{
		"biz_param": map[string]any{
			"detail_page_version":       0,
			"device_level":              cfg.PreloadLevel,
			"disable_digg_stat":         false,
			"disable_video_relate_book": false,
			"need_all_video_definition": true,
			"need_mp4_align":            false,
			"use_os_player":             false,
			"use_server_dns":            false,
			"video_platform":            1024,
		},
		"dr_scene":           cfg.PreloadDrScene,
		"mixed_video_id_map": map[string][]string{"1004": {videoID}},
	})
	if err != nil {
		return providerMedia{}, err
	}
	gz, err := gzipBytes(raw)
	if err != nil {
		return providerMedia{}, err
	}
	pairs := hongguoPlaybackV5Pairs(cfg, identity)

	sawEmpty := false
	var lastErr error = errHongguoPlayerEmptyBody
	for _, host := range hongguoPlaybackV5OrderedHosts(cfg.PreloadHosts) {
		if ctx.Err() != nil {
			return providerMedia{}, ctx.Err()
		}
		signed, err := hongguoPlaybackV5Sign(pairs, time.Now().UnixMilli(), gz, true, cfg.Field21)
		if err != nil {
			return providerMedia{}, err
		}
		url := "https://" + host + cfg.PreloadPath + "?" + signed.Query
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(gz))
		if err != nil {
			return providerMedia{}, err
		}
		request.Header.Set("User-Agent", hongguoPlaybackV5UA)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Encoding", "gzip")
		for k, v := range signed.Headers {
			if v != "" {
				request.Header.Set(k, v)
			}
		}
		content, status, err := downloader.hongguoPlaybackV5Do(ctx, request, 15*time.Second)
		if err != nil {
			if status == http.StatusTooManyRequests || status >= 500 {
				lastErr = fmt.Errorf("红果 preload/v1 HTTP %d", status)
				continue
			}
			return providerMedia{}, err
		}
		if len(content) == 0 {
			sawEmpty = true
			continue
		}
		media, err := downloader.hongguoPlaybackV5Parse(ctx, content, videoID, "preload/v1", true)
		if err != nil {
			return providerMedia{}, err
		}
		hongguoPlaybackV5RememberHost(host)
		return media, nil
	}
	if sawEmpty {
		return providerMedia{}, errHongguoPlayerEmptyBody
	}
	return providerMedia{}, lastErr
}

// resolveHongguoVideoTabV5 —— 兜底通道：GET video_tab/multi_video_model/v1（无请求体）。
func (downloader *Downloader) resolveHongguoVideoTabV5(ctx context.Context, videoID string, identity hongguoPlayerIdentity) (providerMedia, error) {
	cfg := hongguoProtocolCurrent()
	pairs := hongguoPlaybackV5Pairs(cfg, identity)
	pairs = append(pairs,
		[2]string{"video_id", videoID},
		[2]string{"video_id_type", "0"},
		[2]string{"device_level", strconv.Itoa(cfg.TabLevel)},
		[2]string{"need_all_video_definition", "1"},
		[2]string{"need_mp4_align", "0"},
		[2]string{"use_os_player", "0"},
		[2]string{"video_platform", "1024"},
		[2]string{"source", "0"},
		[2]string{"from_video_id", ""},
	)

	sawEmpty := false
	var lastErr error = errHongguoPlayerEmptyBody
	for _, host := range cfg.TabHosts {
		if ctx.Err() != nil {
			return providerMedia{}, ctx.Err()
		}
		signed, err := hongguoPlaybackV5Sign(pairs, time.Now().UnixMilli(), nil, false, cfg.Field21)
		if err != nil {
			return providerMedia{}, err
		}
		url := "https://" + host + cfg.TabPath + "?" + signed.Query
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return providerMedia{}, err
		}
		request.Header.Set("User-Agent", hongguoPlaybackV5TabUA)
		request.Header.Set("Accept", "application/json; charset=utf-8")
		request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		for k, v := range signed.Headers {
			if v != "" {
				request.Header.Set(k, v)
			}
		}
		content, status, err := downloader.hongguoPlaybackV5Do(ctx, request, 15*time.Second)
		if err != nil {
			if status != 0 {
				lastErr = fmt.Errorf("红果 video_tab HTTP %d", status)
				continue
			}
			return providerMedia{}, err
		}
		if len(content) == 0 {
			sawEmpty = true
			continue
		}
		media, err := downloader.hongguoPlaybackV5Parse(ctx, content, videoID, "video_tab", false)
		if err != nil {
			return providerMedia{}, err
		}
		return media, nil
	}
	if sawEmpty {
		return providerMedia{}, errHongguoPlayerEmptyBody
	}
	return providerMedia{}, lastErr
}

// hongguoPlaybackV5Do 发请求并读响应体；status 用于区分「该 host 不可用」与「签名被拒」。
func (downloader *Downloader) hongguoPlaybackV5Do(ctx context.Context, request *http.Request, timeout time.Duration) ([]byte, int, error) {
	response, err := downloader.doPreparedCatalogRequest(request, timeout, nil)
	if err != nil {
		return nil, 0, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return nil, response.StatusCode, readErr
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("红果取流 HTTP %d", response.StatusCode)
	}
	if len(content) > providerMaxBodyBytes {
		return nil, response.StatusCode, errors.New("红果取流响应过大")
	}
	return content, response.StatusCode, nil
}

// hongguoPlaybackV5Parse 解析取流响应：data["1004"][vid] 或 data[vid]，再取 video_model。
func (downloader *Downloader) hongguoPlaybackV5Parse(ctx context.Context, content []byte, videoID, label string, batch bool) (providerMedia, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return providerMedia{}, fmt.Errorf("红果 %s 返回格式异常", label)
	}
	for _, code := range []string{mapString(result, "code", "Code"), mapString(nestedMap(result, "BaseResp"), "StatusCode")} {
		if code != "" && code != "0" {
			return providerMedia{}, fmt.Errorf("红果 %s code=%s", label, truncate(code, 20))
		}
	}
	data := nestedMap(result, "data")
	bucket := nestedMap(data, "1004")
	if len(bucket) == 0 {
		bucket = nestedMap(data, "video_model_data")
	}
	if len(bucket) == 0 {
		bucket = data
	}
	entry := nestedMap(bucket, videoID)
	if len(entry) == 0 {
		if batch {
			entry = bucket
		} else {
			entry = data
		}
	}
	model, err := hongguoAppModel(entry)
	if err != nil {
		return providerMedia{}, err
	}
	if len(model) == 0 {
		return providerMedia{}, fmt.Errorf("红果 %s 缺少请求的分集", label)
	}
	return downloader.hongguoMediaFromModel(ctx, model)
}

// resolveHongguoPlaybackV5 —— 依次用基准设备、本地注册设备跑 preload/v1 与 video_tab/v1。
func (downloader *Downloader) resolveHongguoPlaybackV5(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) || len(videoID) > hongguoPlaybackV5IdMaxLength {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	cfg := hongguoProtocolCurrent()

	identities := make([]hongguoPlayerIdentity, 0, 2)
	if cfg.FallbackID.DeviceID != "" {
		identities = append(identities, cfg.FallbackID)
	}

	sawEmpty := false
	var lastErr error
	for _, identity := range identities {
		media, err := downloader.tryHongguoPlaybackV5Identity(ctx, videoID, identity)
		if err == nil {
			return media, nil
		}
		lastErr = err
		sawEmpty = sawEmpty || errors.Is(err, errHongguoPlayerEmptyBody)
		if ctx.Err() != nil {
			return providerMedia{}, ctx.Err()
		}
	}

	// 基准设备被拒时再试本地注册（可能被上游拒）或自铸设备。
	if id, err := downloader.ensureHongguoPlayerIdentity(ctx); err == nil && id != nil && id.DeviceID != cfg.FallbackID.DeviceID {
		media, err := downloader.tryHongguoPlaybackV5Identity(ctx, videoID, *id)
		if err == nil {
			return media, nil
		}
		lastErr = err
		sawEmpty = sawEmpty || errors.Is(err, errHongguoPlayerEmptyBody)
	}

	if sawEmpty {
		return providerMedia{}, errHongguoPlayerEmptyBody
	}
	if lastErr != nil {
		return providerMedia{}, lastErr
	}
	return providerMedia{}, errors.New("红果取流没有可用设备身份")
}

func (downloader *Downloader) tryHongguoPlaybackV5Identity(ctx context.Context, videoID string, identity hongguoPlayerIdentity) (providerMedia, error) {
	media, err := downloader.resolveHongguoPreloadV5(ctx, videoID, identity)
	if err == nil {
		return media, nil
	}
	if ctx.Err() != nil {
		return providerMedia{}, ctx.Err()
	}
	if !errors.Is(err, errHongguoPlayerEmptyBody) {
		return providerMedia{}, err
	}
	fallback, tabErr := downloader.resolveHongguoVideoTabV5(ctx, videoID, identity)
	if tabErr == nil {
		return fallback, nil
	}
	return providerMedia{}, tabErr
}
