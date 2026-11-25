package core

// 红果取流「当前播放器」方言（field 25 新方言）。
//
// 现状：2026-11-23 起上游把这条方言整体软拒绝（HTTP 200 + 空 body），现行主路径已换成
// provider_hongguo_playback_v5.go（7.4.5.32 / field 21 = 312 / preload/v1）；
// 本文件保留为协议回滚兜底，由 resolveHongguoLegacyPlayerMedia 驱动。
//
// 协议与变换结构逐行移植自 iDuanJu 的 STV/Providers/Hongguo/：
//   - HGCurrentPlayer.swift   —— sign / factor / protobuf / compute / gorgon / helios / ks / transform / mid / medusa
//   - HGCurrentProfile.swift  —— 3428 字节常量（material/header/boxes/initial/checksum/factorIV/factorTable/padding）
//   - HGDevice.swift          —— HGPlayerIdentity.register（xiaomi_8662_64 / 7.3.9.32 / 23127PN0CC 设备身份）
//
// 这些常量与变换都是线上签名协议数据，改动任何一个字节都会导致网关丢弃请求返回空 body。
// 密钥 material(=signKey)/header(=magic)/boxes(=sbox) 与 guoapp 既有 V5 常量一致，其余为新增表。

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
	"sync"
	"time"
)

const (
	hongguoCurrentHost        = "api5-normal-sinfonlinea.fqnovel.com"
	hongguoCurrentPurePath    = "/novel/player/video_model/v1/"
	hongguoCurrentVersionName = "7.3.9.32"
	hongguoCurrentVersionCode = "73932"
	hongguoCurrentChannel     = "xiaomi_8662_64"
	hongguoCurrentDeviceType  = "23127PN0CC"
	hongguoCurrentUA          = "com.phoenix.read/73932 (Linux; U; Android 14; zh_CN; 23127PN0CC; Build/UKQ1.230804.001; Cronet/TTNetVersion:8d40f833 2026-03-03 QuicVersion:21ac1950 2025-11-18)"
)

// errHongguoPlayerEmptyBody 表示网关 HTTP 200 + 0 字节的软拒绝，调用方据此回退 / 重铸设备。
var errHongguoPlayerEmptyBody = errors.New("红果取流返回空 body（签名可能未被接受）")

// ---------------- HGCurrentProfile 派生常量 ----------------

var (
	hongguoCurrentMaterial = hongguoCurrentProfile[0:32]
	hongguoCurrentHeader   = hongguoCurrentProfile[32:48]
	hongguoCurrentBoxes    = hongguoCurrentProfile[48:1072]
	hongguoCurrentInitial  = hongguoCurrentProfile[1072:1088] // 4×4 字节
	hongguoCurrentChecksum = func() [256]uint64 {
		var out [256]uint64
		for i := range out {
			out[i] = binary.LittleEndian.Uint64(hongguoCurrentProfile[1088+i*8:])
		}
		return out
	}()
	hongguoCurrentFactorIV = func() [8]uint32 {
		var out [8]uint32
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(hongguoCurrentProfile[3136+i*4:])
		}
		return out
	}()
	hongguoCurrentFactorTable = func() [64]uint32 {
		var out [64]uint32
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(hongguoCurrentProfile[3168+i*4:])
		}
		return out
	}()
	hongguoCurrentPadding = hongguoCurrentProfile[3424:3428]
)

// ---------------- 基础原语 ----------------

func hongguoCurrentRor64(x uint64, n uint64) uint64 { return bits.RotateLeft64(x, -int(n&63)) }

func hongguoCurrentNibbleSwap(b byte) byte { return (b&0x0f)<<4 | (b >> 4) }

func hongguoCurrentBE32(v uint32) [4]byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b
}

func hongguoCurrentLE32(v uint32) [4]byte { return le32(v) }

// hongguoCurrentEncodeQuery 复用 guoapp 既有的 percent-encoding（保留 -_.~* 与字母数字，其余大写十六进制）。
// 与 Swift HGPlayerSign.encodeQuery 对当前查询值集合等价。
func hongguoCurrentEncodeQuery(pairs [][2]string) string {
	var sb bytes.Buffer
	for i, p := range pairs {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(hongguoV5EncodeComponent(p[0]))
		sb.WriteByte('=')
		sb.WriteString(hongguoV5EncodeComponent(p[1]))
	}
	return sb.String()
}

func hongguoCurrentBitReverse(b byte) byte { return bits.Reverse8(b) }

// ---------------- HGCurrentPlayer.sign ----------------

type hongguoCurrentSigned struct {
	Query   string
	Headers map[string]string
	Ticket  int64
}

// hongguoCurrentSign 生成「当前播放器」取流签名。pairs 必须保持 purePlayerPairs 的固定顺序。
func hongguoCurrentSign(pairs [][2]string, nowMillis int64, deviceID, versionName string, body []byte) (hongguoCurrentSigned, error) {
	var signed hongguoCurrentSigned
	if nowMillis < 1000 || deviceID == "" || len(deviceID) > 64 || versionName == "" || len(versionName) > 32 {
		return signed, errors.New("红果当前播放器签名参数无效")
	}
	ent := make([]byte, 24)
	if _, err := rand.Read(ent); err != nil {
		return signed, err
	}
	base := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		if p[0] != "ts" && p[0] != "_rticket" {
			base = append(base, p)
		}
	}
	digest := md5Bytes(body)
	for offset := int64(0); offset < 128; offset++ {
		ticket := nowMillis + offset
		t := uint32(ticket / 1000)
		queryPairs := make([][2]string, 0, len(base)+2)
		queryPairs = append(queryPairs, base...)
		queryPairs = append(queryPairs, [2]string{"ts", strconv.FormatUint(uint64(t), 10)}, [2]string{"_rticket", strconv.FormatInt(ticket, 10)})
		query := hongguoCurrentEncodeQuery(queryPairs)
		if len(query) > 32768 {
			return signed, errors.New("红果当前播放器查询过长")
		}
		hash := hongguoSM3([]byte(query))
		input := make([]byte, 0, 52)
		input = append(input, hash[:]...)
		input = append(input, digest[:]...)
		tt := le32(t)
		input = append(input, tt[:]...)
		selector := int(hongguoGetIV(0x20230928, input) & 15)
		if selector%3 != 0 {
			continue
		}
		factor, err := hongguoCurrentFactor(input, t, selector)
		if err != nil {
			return signed, err
		}
		nonce32 := binary.LittleEndian.Uint32(ent[0:4])
		pb := hongguoCurrentProtobuf(t, deviceID, versionName, nonce32, factor, hash[:6])
		headers, err := hongguoCurrentCompute(
			query, digest[:], pb, hongguoCurrentMaterial, "646637794", "8662", uint64(t),
			uint16(ent[0])|uint16(ent[1])<<8, 0x101,
			binary.LittleEndian.Uint32(ent[4:8]),
			binary.LittleEndian.Uint32(ent[8:12]),
			binary.LittleEndian.Uint32(ent[12:16]),
			0x10000000, binary.LittleEndian.Uint32(ent[16:20]),
		)
		if err != nil {
			return signed, err
		}
		headers["Accept"] = "application/json; charset=utf-8,application/x-protobuf"
		headers["Content-Type"] = "application/json; charset=utf-8"
		headers["x-xs-from-web"] = "0"
		headers["sdk-version"] = "2"
		headers["x-ss-req-ticket"] = strconv.FormatInt(ticket, 10)
		headers["x-ss-stub"] = hex.EncodeToString(digest[:])
		return hongguoCurrentSigned{Query: query, Headers: headers, Ticket: ticket}, nil
	}
	return signed, errors.New("红果当前播放器签名未找到可用 selector")
}

func hongguoCurrentProtobuf(t uint32, deviceID, version string, nonce uint32, factor, hash []byte) []byte {
	n := func(tag uint32, v int64) hongguoProtoField { return hongguoProtoSint(tag, v) }
	s := func(tag uint32, v string) hongguoProtoField { return hongguoProtoString(tag, v) }
	b := func(tag uint32, v []byte) hongguoProtoField { return hongguoProtoBytes(tag, v) }

	env := hongguoProto([]hongguoProtoField{
		n(1, 100), n(5, 7), s(6, "v04.10.03.04-bugfix"), n(7, 10001), s(14, version),
	})
	nested15 := hongguoProto([]hongguoProtoField{n(1, 1), n(2, 1), n(3, 1)})
	var zero8 [8]byte

	return hongguoProto([]hongguoProtoField{
		b(1, hongguoCurrentHeader),
		n(2, 5),
		n(3, int64(nonce)),
		s(4, "8662"),
		s(5, deviceID),
		s(6, "646637794"),
		s(7, version),
		s(8, "v04.10.03-ml-android"),
		n(9, 135529984/2),
		b(10, zero8[:]),
		n(12, int64(t)),
		b(13, factor),
		b(14, hash),
		b(15, nested15),
		n(17, int64(t)),
		s(20, "none"),
		n(21, 377),
		b(23, env),
		s(24, `{"cmr":16777216,"cmr2":16777216,"vpn":0,"kd":0,"dyn":"","do":0,"tk":false}`),
		// 匿名批量端点要求 field 25 非空（2026-11-21 起）。
		s(25, "AAAAAAAAAAAAAAAAAAAAAAAAA"),
	})
}

// ---------------- factor（SHA-256 式 + 新表） ----------------

func hongguoCurrentFactor(input []byte, seconds uint32, selector int) ([]byte, error) {
	if len(input) != 52 || !slicesContainInt([]int{0, 3, 6, 9, 12, 15}, selector) {
		return nil, errors.New("红果当前播放器签名参数无效")
	}
	type cfg struct{ a, b uint32 }
	cfgs := [6]cfg{{15, 100}, {13, 96}, {62, 94}, {79, 97}, {66, 96}, {127, 99}}
	c := cfgs[selector/3]

	rotation := seconds + c.a - 15 + 0xee47628f
	ini := make([]uint32, 8)
	for i := 0; i < 8; i++ {
		ini[i] = rotr32(hongguoCurrentFactorIV[i], rotation&31)
	}
	table := make([]uint32, 64)
	for i := 0; i < 64; i++ {
		table[i] = rotr32(hongguoCurrentFactorTable[i], (rotation+1)&31)
	}

	block := make([]byte, 64)
	copy(block, input)
	shift := (rotation + 2) & 7
	for i := 0; i < 4; i++ {
		x := uint32(hongguoCurrentPadding[i])
		block[52+i] = byte(x>>shift | x<<((8-shift)&7))
	}
	block[62] = 1
	block[63] = 0xa0

	w := make([]uint32, 128)
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(block[i*4:])
	}
	for i := 16; i < 128; i++ {
		x, y := w[i-15], w[i-2]
		w[i] = w[i-16] + (rotr32(x, 7) ^ rotr32(x, 18) ^ (x >> 3)) + w[i-7] + (rotr32(y, 17) ^ rotr32(y, 19) ^ (y >> 10))
	}

	v := ini
	sig := func(x uint32) uint32 { return rotr32(x, 6) ^ rotr32(x, 11) ^ rotr32(x, 25) }
	upper := func(x uint32) uint32 { return rotr32(x, 2) ^ rotr32(x, 13) ^ rotr32(x, 22) }
	for round := uint32(0); round < c.b; round++ {
		a, b_, cc, d, e, f, g, h := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]
		idx := (c.a + round)
		kw := table[idx&63] + w[idx&127]
		var t uint32
		switch selector {
		case 0:
			t = kw + (cc ^ (e & (f ^ cc))) + sig(e) + g
			v = []uint32{t + h, a, b_, cc, d, e, f, ((a & b_) | ((b_ | a) & d)) + upper(a) + t}
		case 3:
			t = kw + (h ^ (a & (cc ^ h))) + sig(a) + b_
			v = []uint32{h, a, ((f & d) | ((d | f) & g)) + upper(f) + t, cc, d, t + e, f, g}
		case 6:
			t = kw + (a ^ (d & (b_ ^ a))) + sig(d) + cc
			v = []uint32{h, a, b_, ((h & g) | ((g | h) & f)) + upper(h) + t, d, t + e, f, g}
		case 9:
			t = kw + (b_ ^ (cc & (f ^ b_))) + sig(cc) + d
			v = []uint32{h, t + a, b_, cc, ((g & h) | ((h | g) & e)) + upper(g) + t, e, f, g}
		case 12:
			t = kw + (e ^ (b_ & (a ^ e))) + sig(b_) + g
			v = []uint32{t + h, a, b_, cc, d, e, f, ((d & f) | ((f | d) & cc)) + upper(d) + t}
		default: // 15
			t = kw + (a ^ (b_ & (d ^ a))) + sig(b_) + f
			v = []uint32{h, a, b_, cc, d, t + e, ((g & cc) | ((cc | g) & h)) + upper(g) + t, g}
		}
	}

	digest := make([]byte, 32)
	for i := 0; i < 8; i++ {
		be := hongguoCurrentBE32(ini[i] + v[i])
		copy(digest[i*4:], be[:])
	}
	out := make([]byte, 20)
	for i := 0; i < 16; i++ {
		out[i] = digest[i] ^ digest[i+16]
	}
	state := uint32(0x20220420)
	for i := 0; i < 8; i++ {
		x := uint32(out[i])
		if i%2 == 1 {
			state = ^(state ^ ((state << 11) | x) ^ (state >> 5))
		} else {
			state = state ^ (state << 7) ^ x ^ (state >> 3)
		}
	}
	tail := le32((state & ^uint32(0x24)) ^ 0x01000000)
	copy(out[16:], tail[:])
	return out, nil
}

func slicesContainInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------- compute ----------------

func hongguoCurrentCompute(query string, digest, pb, material []byte, license, aid string, t uint64, nonce, flags uint16, heliosRandom, random1, random2 uint32, local uint64, previous uint32) (map[string]string, error) {
	if len(material) != 32 || len(digest) != 16 || len(pb) < 2 || len(pb) > 32768 {
		return nil, errors.New("红果当前播放器签名参数无效")
	}
	var crc uint64
	for _, ch := range []byte(license) {
		crc = hongguoCurrentChecksum[byte(crc)^ch] ^ (crc >> 8)
	}
	state := uint32(hongguoCurrentRor64(crc^t^local, 60)) ^ previous

	argus := le32(uint32(t))
	ladon := hongguoCurrentBE32(state)
	headers := map[string]string{
		"x-khronos": strconv.FormatUint(t, 10),
		"x-argus":   base64.StdEncoding.EncodeToString(argus[:]),
		"x-ladon":   base64.StdEncoding.EncodeToString(ladon[:]),
		"x-gorgon":  hongguoCurrentGorgon(query, digest, t, nonce, flags),
		"x-helios":  hongguoCurrentHelios(t, license, aid, heliosRandom),
	}
	medusa, err := hongguoCurrentMedusa(pb, material, uint64(t), random1, random2)
	if err != nil {
		return nil, err
	}
	headers["x-medusa"] = medusa
	return headers, nil
}

func hongguoCurrentGorgon(query string, digest []byte, t uint64, nonce, flags uint16) string {
	key := [8]byte{0x4a, byte(flags & 255), 0x16, byte(nonce >> 8), 0x47, 0x6c, byte(flags >> 8), byte(nonce & 255)}
	var table [256]int
	for i := range table {
		table[i] = i
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + table[i] + int(key[i&7])) & 255
		table[i] = table[j]
	}
	q := md5Bytes([]byte(query))
	seed := make([]byte, 0, 20)
	seed = append(seed, q[:4]...)
	seed = append(seed, digest[:4]...)
	seed = append(seed, 0, 0, 0, 0)
	seed = append(seed, 0, 9, 9, 4)
	be := hongguoCurrentBE32(uint32(t))
	seed = append(seed, be[:]...)

	out := make([]byte, 20)
	j = 0
	for n := 0; n < 20; n++ {
		i := n + 1
		j = (j + table[i&255]) & 255
		y := table[j&255]
		table[i&255] = y
		out[n] = seed[n] ^ byte(table[(y+y)&255])
	}
	for i := 0; i < 20; i++ {
		x := out[i]
		swapped := (x >> 4) | (x << 4)
		v := out[(i+1)%20] ^ swapped
		out[i] = ^(bits.Reverse8(v) ^ 20)
	}
	head := []byte{0x84, 4, byte(nonce), byte(nonce >> 8), byte(flags), byte(flags >> 8)}
	return hex.EncodeToString(append(head, out...))
}

func hongguoCurrentHelios(t uint64, license, aid string, random uint32) string {
	randLE := hongguoCurrentLE32(random)
	keyInput := append(randLE[:], []byte(aid)...)
	md5hex := md5Bytes(keyInput)
	keyBytes := []byte(hex.EncodeToString(md5hex[:]))

	keys := make([]uint64, 35)
	keys[0] = binary.LittleEndian.Uint64(keyBytes[0:8])
	a := keys[0]
	b := binary.LittleEndian.Uint64(keyBytes[8:16])
	queue := []uint64{binary.LittleEndian.Uint64(keyBytes[16:24]), binary.LittleEndian.Uint64(keyBytes[24:32])}
	for i := uint64(0); i < 34; i++ {
		x := (hongguoCurrentRor64(b, 8) + a) ^ i
		queue = append(queue, x)
		x ^= hongguoCurrentRor64(a, 61)
		keys[i+1] = x
		a = x
		b = queue[0]
		queue = queue[1:]
	}

	raw := []byte(strconv.FormatUint(t, 10) + "-" + license + "-" + aid)
	n := 16 - len(raw)%16
	plain := make([]byte, 0, len(raw)+n)
	plain = append(plain, raw...)
	for i := 0; i < n; i++ {
		plain = append(plain, byte(n))
	}

	out := append([]byte(nil), randLE[:]...)
	for at := 0; at < len(plain); at += 16 {
		l := binary.LittleEndian.Uint64(plain[at:])
		r := binary.LittleEndian.Uint64(plain[at+8:])
		for i := 0; i < 34; i++ {
			r = keys[i] ^ (l + hongguoCurrentRor64(r, 8))
			l = r ^ hongguoCurrentRor64(l, 61)
		}
		out = binary.LittleEndian.AppendUint64(out, l)
		out = binary.LittleEndian.AppendUint64(out, r)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// ---------------- medusa（ks + transform + mid） ----------------

func hongguoCurrentKS(pb, mat, t []byte) []byte {
	seed := make([]byte, 0, len(mat)+len(t)+len(mat))
	seed = append(seed, mat...)
	seed = append(seed, t...)
	seed = append(seed, mat...)
	box := hongguoSM3(seed)

	n := len(pb)
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		a := uint32(box[(4*i)%32])
		c := uint32(box[(4*i+1)%32])
		x := uint32(pb[i])
		w := ^((((x >> 4) | (x << 4)) + a) ^ c)
		rot := ((w << 3) & 0xfffffff8) | ((w >> 5) & 7)
		b[n-1-i] = byte(^(a ^ (rot + c)))
	}
	last := b[n-1]
	prev := b[n-2]
	b[0] = b[0] + ^(last ^ prev)
	b[1] = (b[0] ^ last ^ byte(254)) + b[1]
	for j := 2; j < n-1; j++ {
		v := uint32(b[j-1])
		rot := ((v >> 5) & 7) | (v << 3)
		b[j] = b[j] + byte(^(uint32(b[j-2]) ^ rot ^ uint32(j)))
	}
	b[n-1] = b[n-1] ^ b[n-2]
	var sum byte
	for _, v := range b[1:] {
		sum += v
	}
	b[0] = (b[0] ^ b[1]) + sum

	reg := ^(((uint32(t[2]) << 11) | uint32(t[3])) ^ uint32(t[2]) ^ (uint32(t[2]) >> 5))
	mask := hongguoCurrentBE32(reg)
	stream := append(make([]byte, 8), b...)
	out := make([]byte, len(stream))
	for i := range stream {
		out[i] = mask[i%4] ^ stream[len(stream)-1-i]
	}
	return out
}

var hongguoCurrentCon = [4][4]int{{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0}}

var hongguoCurrentOrder = [4][16]int{
	{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
	{0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7},
	{0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7},
	{0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7},
}

func hongguoCurrentTransform(plain, key, iv []byte, branch int) ([]byte, error) {
	if len(key) != 16 || len(iv) != 16 || branch < 0 || branch > 3 {
		return nil, errors.New("红果当前播放器签名参数无效")
	}
	box := hongguoCurrentBoxes[branch*256 : (branch+1)*256]
	initial := hongguoCurrentInitial[branch*4 : branch*4+4]
	ini := binary.LittleEndian.Uint32(initial)

	expanded := make([]byte, 48)
	for i := 0; i < 16; i++ {
		expanded[i] = key[i] ^ initial[i%4]
	}
	rounds := 8
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i&3 == 0 {
			old := a
			a = byte((ini >> (rounds & 24)) ^ uint32(box[int(b)]))
			b = box[int(c)]
			c = box[int(d)]
			d = box[int(old)]
		}
		rounds += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}

	pad := 16 - len(plain)%16
	source := make([]byte, 0, len(plain)+pad)
	source = append(source, plain...)
	for i := 0; i < pad; i++ {
		source = append(source, byte(pad))
	}

	co := hongguoCurrentCon[branch]
	order := hongguoCurrentOrder[branch]
	previous := append([]byte(nil), iv...)
	var out []byte
	for at := 0; at < len(source); at += 16 {
		state := make([]byte, 16)
		for i := 0; i < 16; i++ {
			keyIndex := 4*(i/4) + co[i%4]
			state[i] = source[at+i] ^ previous[i] ^ expanded[keyIndex]
		}
		for round := 1; round <= 2; round++ {
			old := append([]byte(nil), state...)
			for i := 0; i < 16; i++ {
				index := co[i/4]*4 + i%4
				state[i] = box[int(old[index])]
			}
			old = append([]byte(nil), state...)
			for i := 0; i < 16; i++ {
				state[i] = old[order[i]]
			}
			if round == 1 {
				old = append([]byte(nil), state...)
				for i := 0; i < 16; i++ {
					index := 4*(i/4) + co[i%4]
					state[i] = old[index]
				}
				for j := 0; j < 4; j++ {
					total := state[j] ^ state[4+j] ^ state[8+j] ^ state[12+j]
					first := state[j]
					for i := 0; i < 4; i++ {
						next := first
						if i < 3 {
							next = state[(i+1)*4+j]
						}
						x := state[i*4+j] ^ next
						xt := x << 1
						if x&128 != 0 {
							xt ^= 0x1b
						}
						state[i*4+j] ^= total ^ xt
					}
				}
			}
			for i := 0; i < 16; i++ {
				state[i] ^= expanded[16*round+4*(i/4)+co[i%4]]
			}
		}
		for i := 0; i < 16; i++ {
			previous[i] = state[i] ^ expanded[16+i]
		}
		out = append(out, previous...)
	}
	return out, nil
}

// hongguoCurrentMid 解析 protobuf，取 field 13 / 14 的首字节拼成 UInt16。
func hongguoCurrentMid(pb []byte) (uint16, error) {
	first := [2]int{-1, -1}
	at := 0
	read := func() (uint64, error) {
		var v uint64
		for i := 0; i < 10; i++ {
			if at >= len(pb) {
				return 0, errors.New("红果当前播放器签名参数无效")
			}
			b := pb[at]
			at++
			if i == 9 && b > 1 {
				return 0, errors.New("红果当前播放器签名参数无效")
			}
			v |= uint64(b&127) << (7 * i)
			if b&128 == 0 {
				return v, nil
			}
		}
		return 0, errors.New("红果当前播放器签名参数无效")
	}
	for at < len(pb) {
		tag, err := read()
		if err != nil {
			return 0, err
		}
		if tag>>3 == 0 {
			return 0, errors.New("红果当前播放器签名参数无效")
		}
		switch tag & 7 {
		case 0:
			if _, err := read(); err != nil {
				return 0, err
			}
		case 1:
			at += 8
		case 5:
			at += 4
		case 2:
			n64, err := read()
			if err != nil {
				return 0, err
			}
			if n64 > uint64(len(pb)-at) {
				return 0, errors.New("红果当前播放器签名参数无效")
			}
			n := int(n64)
			field := int(tag >> 3)
			if field >= 13 && field <= 14 && n > 0 {
				first[field-13] = int(pb[at])
			}
			at += n
		default:
			return 0, errors.New("红果当前播放器签名参数无效")
		}
		if at > len(pb) {
			return 0, errors.New("红果当前播放器签名参数无效")
		}
	}
	if first[0] < 0 || first[1] < 0 {
		return 0, errors.New("红果当前播放器签名参数无效")
	}
	return uint16(first[0]&63) | uint16(first[1]&63)<<6, nil
}

// hongguoCurrentPrefix —— medusa 头字节前缀折叠（注意与 hongguoGetIV 不同，是另一组移位常量）。
func hongguoCurrentPrefix(mat []byte) uint32 {
	var prefix uint32
	for i, b := range mat {
		x := uint32(b)
		if i%2 == 0 {
			prefix = prefix ^ (prefix << 7) ^ x ^ (prefix >> 3)
		} else {
			prefix = prefix ^ ^((prefix << 11) ^ x ^ (prefix >> 5))
		}
	}
	return prefix
}

func hongguoCurrentMedusa(pb, mat []byte, t uint64, r1, r2 uint32) (string, error) {
	m, err := hongguoCurrentMid(pb)
	if err != nil {
		return "", err
	}
	t4 := le32(r1)
	prefix := hongguoCurrentPrefix(mat)

	pre := make([]byte, 0, 256)
	pre = append(pre, byte(prefix))
	r2b := le32(r2)
	pre = append(pre, r2b[:]...)
	pre = append(pre, 1, byte(m), byte(m>>8), 0x18)
	pre = append(pre, hongguoCurrentKS(pb, mat, t4[:])...)
	pre = append(pre, t4[2], t4[3])
	if len(pre) < 248 {
		return "", errors.New("红果当前播放器签名参数无效")
	}

	lane := [8]byte{6, 5, 3, 4, 2, 7, 1, 0}
	bitsArr := [8]byte{1, 7, 3, 6, 2, 0, 4, 5}
	plain := make([]byte, 31)
	for i := 0; i < 248; i++ {
		plain[i/8] |= ((pre[i] >> lane[i%8]) & 1) << bitsArr[i%8]
	}

	mdKey := md5Bytes(mat[0:16])
	mdIV := md5Bytes(mat[16:32])
	channel, err := hongguoCurrentTransform(plain, mdKey[:], mdIV[:], int(t&3))
	if err != nil {
		return "", err
	}

	body := append([]byte(nil), pre...)
	for i := 0; i < 248; i++ {
		body[i] = (body[i] &^ (1 << lane[i%8])) | (((channel[i/8] >> bitsArr[i%8]) & 1) << lane[i%8])
	}

	ver5 := hongguoCurrentLE32(5)
	hdr := append(ver5[:], hongguoCurrentHeader...)
	mask := le32(uint32(t))
	for i := range hdr {
		hdr[i] ^= mask[i%4]
	}

	output := make([]byte, 0, len(hdr)+2+3+len(body))
	output = append(output, hdr...)
	output = append(output, t4[0], t4[1])
	output = append(output, 0, 1, channel[31])
	output = append(output, body...)
	return base64.StdEncoding.EncodeToString(output), nil
}

// ---------------- 设备身份（HGPlayerIdentity.register） ----------------

type hongguoPlayerIdentity struct {
	DeviceID     string
	InstallID    string
	CDID         string
	OpenUDID     string
	RegisteredAt time.Time
}

func (id *hongguoPlayerIdentity) usable(now time.Time) bool {
	return id != nil && id.DeviceID != "" && id.InstallID != "" && id.CDID != "" && id.OpenUDID != "" &&
		now.Sub(id.RegisteredAt) < 7*time.Minute
}

var (
	hongguoPlayerIdentityMu           sync.Mutex
	hongguoPlayerIdentityState        *hongguoPlayerIdentity
	hongguoPlayerIdentityRejected     = map[string]bool{}
	hongguoPlayerIdentityBlockedUntil time.Time
	hongguoPlayerIdentityRegistering  bool
)

func (downloader *Downloader) ensureHongguoPlayerIdentity(ctx context.Context) (*hongguoPlayerIdentity, error) {
	hongguoPlayerIdentityMu.Lock()
	defer hongguoPlayerIdentityMu.Unlock()
	now := time.Now()
	if hongguoPlayerIdentityState != nil && hongguoPlayerIdentityState.usable(now) && !hongguoPlayerIdentityRejected[hongguoPlayerIdentityState.DeviceID] {
		return hongguoPlayerIdentityState, nil
	}
	if hongguoPlayerIdentityRegistering {
		return nil, errors.New("红果播放器设备注册进行中")
	}
	if now.Before(hongguoPlayerIdentityBlockedUntil) {
		return nil, errors.New("红果播放器设备注册冷却中，请稍后重试")
	}
	hongguoPlayerIdentityRegistering = true
	defer func() { hongguoPlayerIdentityRegistering = false }()
	id, err := downloader.registerHongguoPlayerIdentity(ctx)
	if err != nil {
		hongguoPlayerIdentityBlockedUntil = now.Add(30 * time.Second)
		return nil, err
	}
	hongguoPlayerIdentityState = &id
	hongguoPlayerIdentityRejected = map[string]bool{}
	return hongguoPlayerIdentityState, nil
}

// remintHongguoPlayerIdentity 在空 body 软拒绝后丢弃当前身份，强制下次重新注册。
func remintHongguoPlayerIdentity() {
	hongguoPlayerIdentityMu.Lock()
	defer hongguoPlayerIdentityMu.Unlock()
	if hongguoPlayerIdentityState != nil {
		hongguoPlayerIdentityRejected[hongguoPlayerIdentityState.DeviceID] = true
	}
	hongguoPlayerIdentityState = nil
}

func (downloader *Downloader) registerHongguoPlayerIdentity(ctx context.Context) (hongguoPlayerIdentity, error) {
	var id hongguoPlayerIdentity
	id.CDID = uuidLike()
	id.OpenUDID = randomHex(8)
	query := "aid=8662&app_name=novelread&version_code=" + hongguoCurrentVersionCode + "&version_name=" + hongguoCurrentVersionName +
		"&device_platform=android&os=android&ssmix=a&language=zh&channel=" + hongguoCurrentChannel +
		"&device_type=" + hongguoCurrentDeviceType + "&device_brand=Xiaomi&resolution=1200*2670&dpi=460&host_abi=arm64-v8a" +
		"&manifest_version_code=" + hongguoCurrentVersionCode + "&update_version_code=" + hongguoCurrentVersionCode +
		"&ac=wifi&app_type=normal&cpu_support64=true&os_api=34&os_version=14&cdid=" + id.CDID + "&openudid=" + id.OpenUDID

	header := map[string]any{
		"display_name": "红果短剧", "aid": 8662, "channel": hongguoCurrentChannel, "package": "com.phoenix.read",
		"app_version": hongguoCurrentVersionName, "version_code": 73932, "manifest_version_code": 73932, "update_version_code": 73932,
		"sdk_version": "3.9.5", "sdk_target_version": 34, "os": "Android", "os_version": "14", "os_api": 34,
		"device_model": hongguoCurrentDeviceType, "device_brand": "Xiaomi", "device_manufacturer": "Xiaomi", "cpu_abi": "arm64-v8a",
		"release_build": "UKQ1.230804.001", "density_dpi": 460, "display_density": "xxhdpi", "resolution": "2670x1200",
		"language": "zh", "timezone": 8, "region": "CN", "tz_name": "Asia/Shanghai", "cdid": id.CDID, "openudid": id.OpenUDID,
		"clientudid": uuidLike(), "google_aid": "", "req_id": uuidLike(),
	}
	body, err := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": time.Now().Unix()})
	if err != nil {
		return id, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://log.snssdk.com/service/2/device_register/", bytes.NewReader(body))
	if err != nil {
		return id, err
	}
	request.Header.Set("User-Agent", hongguoCurrentUA)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("sdk-version", "2")
	response, err := downloader.doPreparedCatalogRequest(request, 15*time.Second, func(prepared *http.Request) {
		now := time.Now()
		ts := uint32(now.Unix())
		prepared.URL.RawQuery = query
		for k, v := range hongguoBasicHeaders(query, body, ts) {
			prepared.Header.Set(k, v)
		}
	})
	if err != nil {
		return id, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if readErr != nil {
		return id, readErr
	}
	if response.StatusCode != http.StatusOK {
		return id, fmt.Errorf("红果播放器设备注册 HTTP %d", response.StatusCode)
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return id, errors.New("红果播放器设备注册返回格式异常")
	}
	id.DeviceID = firstNonEmpty(mapString(result, "device_id_str", "device_id"))
	id.InstallID = firstNonEmpty(mapString(result, "install_id_str", "install_id"))
	if id.InstallID == "" || id.InstallID == "0" {
		id.InstallID = mapString(result, "iid")
	}
	if !hongguoNumericID.MatchString(id.DeviceID) || !hongguoNumericID.MatchString(id.InstallID) || id.DeviceID == "0" || id.InstallID == "0" {
		return id, errors.New("红果播放器设备注册未返回设备号")
	}
	id.RegisteredAt = time.Now()
	return id, nil
}

// hongguoBasicHeaders —— device_register 用的简单 X-Gorgon（flags 0x4481）+ X-Khronos + X-Argus。
func hongguoBasicHeaders(params string, body []byte, ts uint32) map[string]string {
	var variantBytes [2]byte
	_, _ = rand.Read(variantBytes[:])
	variant := binary.LittleEndian.Uint16(variantBytes[:])
	argus := le32(ts)
	return map[string]string{
		"X-Khronos": strconv.FormatUint(uint64(ts), 10),
		"X-Gorgon":  hongguoXGorgon(params, ts, variant, body, 0x4481),
		"X-Argus":   base64.StdEncoding.EncodeToString(argus[:]),
	}
}

func hongguoXGorgon(params string, ts uint32, variant uint16, body []byte, flags uint16) string {
	var chunkX [4]byte
	if len(body) > 0 {
		d := md5Bytes(body)
		copy(chunkX[:], d[:4])
	}
	q := md5Bytes([]byte(params))
	concat := make([]byte, 0, 20)
	concat = append(concat, q[:4]...)
	concat = append(concat, chunkX[:]...)
	concat = append(concat, 0, 0, 0, 0)
	concat = append(concat, 0x00, 0x03, 0x0a, 0x04)
	tb := hongguoCurrentBE32(ts)
	concat = append(concat, tb[:]...)

	fh := byte(flags >> 8)
	fl := byte(flags & 0xff)
	key := [8]byte{0x4a, fh, 0x16, byte(variant & 0xff), 0x47, 0x6c, fl, byte(variant >> 8)}
	ks := hongguoBrokenRC4(key[:], 20)
	xored := make([]byte, 20)
	for i := 0; i < 20; i++ {
		xored[i] = concat[i] ^ ks[i]
	}
	head := []byte{0x84, 0x04, byte(variant >> 8), byte(variant & 0xff), fh, fl}
	return hex.EncodeToString(append(head, hongguoGorgon20(xored)...))
}

func hongguoBrokenRC4(key []byte, n int) []byte {
	S := make([]int, 256)
	for i := range S {
		S[i] = i
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + S[i] + int(key[i&7])) & 0xff
		S[i] = S[j]
	}
	i := 0
	jj := 0
	out := make([]byte, n)
	for k := 0; k < n; k++ {
		i = (i + 1) & 0xff
		jj = (jj + S[i]) & 0xff
		v := S[jj]
		S[i] = v
		out[k] = byte(S[(S[i]+v)&0xff])
	}
	return out
}

func hongguoGorgon20(seed []byte) []byte {
	buf := append([]byte(nil), seed...)
	for n := 0; n < 20; n++ {
		buf[n] = hongguoCurrentNibbleSwap(buf[n])
		x := buf[n] ^ buf[(n+1)%20]
		buf[n] = byte(0x14) ^ ^bits.Reverse8(x)
	}
	return buf
}

// ---------------- 请求与解析 ----------------

func hongguoCurrentPairs(id *hongguoPlayerIdentity) [][2]string {
	vc := hongguoCurrentVersionCode
	return [][2]string{
		{"iid", id.InstallID}, {"device_id", id.DeviceID}, {"ac", "wifi"}, {"channel", hongguoCurrentChannel}, {"aid", "8662"},
		{"app_name", "novelread"}, {"cdid", id.CDID}, {"version_code", vc}, {"version_name", hongguoCurrentVersionName},
		{"device_platform", "android"}, {"os", "android"}, {"ssmix", "a"}, {"device_type", hongguoCurrentDeviceType},
		{"device_brand", "Xiaomi"}, {"language", "zh"}, {"os_api", "34"}, {"os_version", "14"},
		{"manifest_version_code", vc}, {"resolution", "1200*2670"}, {"dpi", "460"}, {"update_version_code", vc},
		{"host_abi", "arm64-v8a"}, {"dragon_device_type", "phone"}, {"pv_player", vc}, {"compliance_status", "0"},
		{"need_personal_recommend", "1"}, {"player_so_load", "1"}, {"is_android_pad_screen", "0"}, {"openudid", id.OpenUDID},
	}
}

func (downloader *Downloader) hongguoCurrentPOST(ctx context.Context, path string, signed hongguoCurrentSigned, body []byte) ([]byte, error) {
	url := "https://" + hongguoCurrentHost + path + "?" + signed.Query
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range signed.Headers {
		if v != "" {
			request.Header.Set(k, v)
		}
	}
	request.Header.Set("User-Agent", hongguoCurrentUA)
	request.Header.Set("x-ss-dp", "8662")
	request.Header.Set("lc", "101")
	request.Header.Set("x-reading-request", strconv.FormatInt(signed.Ticket, 10)+"-"+strconv.FormatInt(time.Now().UnixMilli()&0x7fffffff, 10))

	response, err := downloader.doPreparedCatalogRequest(request, 20*time.Second, nil)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果当前播放器取流 HTTP %d", response.StatusCode)
	}
	if len(content) == 0 {
		return nil, errHongguoPlayerEmptyBody
	}
	if len(content) > providerMaxBodyBytes {
		return nil, errors.New("红果当前播放器取流响应过大")
	}
	return content, nil
}

// resolveHongguoCurrentPure —— 单集 video_model/v1（field 25 新方言）。
func (downloader *Downloader) resolveHongguoCurrentPure(ctx context.Context, videoID string, level int, pairs [][2]string) (providerMedia, error) {
	if level >= 4 {
		level = 4
	} else {
		level = 3
	}
	bodyObj := map[string]any{
		"video_id": videoID,
		"biz_param": map[string]any{
			"video_id_type":             0,
			"need_all_video_definition": true,
			"video_platform":            3,
			"device_level":              level,
		},
	}
	body, err := json.Marshal(bodyObj)
	if err != nil {
		return providerMedia{}, err
	}
	did := ""
	for _, p := range pairs {
		if p[0] == "device_id" {
			did = p[1]
		}
	}
	signed, err := hongguoCurrentSign(pairs, time.Now().UnixMilli(), did, hongguoCurrentVersionName, body)
	if err != nil {
		return providerMedia{}, err
	}
	content, err := downloader.hongguoCurrentPOST(ctx, hongguoCurrentPurePath, signed, body)
	if err != nil {
		return providerMedia{}, err
	}
	return downloader.hongguoCurrentParseModel(ctx, content, videoID, false)
}

// resolveHongguoCurrentBatch —— 批量 multi_video_model（AB-off / AB-on 两条路径，同为 field 25 新方言）。
func (downloader *Downloader) resolveHongguoCurrentBatch(ctx context.Context, videoID string, level int, pairs [][2]string, path string) (providerMedia, error) {
	bodyObj := map[string]any{
		"biz_param": map[string]any{
			"detail_page_version":       0,
			"device_level":              level,
			"disable_digg_stat":         false,
			"disable_video_relate_book": false,
			"need_all_video_definition": true,
			"need_mp4_align":            false,
			"use_os_player":             false,
			"use_server_dns":            true,
			"use_server_dns_scene":      "cold_start_stage",
			"video_platform":            1024,
		},
		"dr_scene":           "player",
		"mixed_video_id_map": map[string][]string{"1004": {videoID}},
	}
	body, err := json.Marshal(bodyObj)
	if err != nil {
		return providerMedia{}, err
	}
	did := ""
	for _, p := range pairs {
		if p[0] == "device_id" {
			did = p[1]
		}
	}
	signed, err := hongguoCurrentSign(pairs, time.Now().UnixMilli(), did, hongguoCurrentVersionName, body)
	if err != nil {
		return providerMedia{}, err
	}
	content, err := downloader.hongguoCurrentPOST(ctx, path, signed, body)
	if err != nil {
		return providerMedia{}, err
	}
	return downloader.hongguoCurrentParseModel(ctx, content, videoID, true)
}

func (downloader *Downloader) hongguoCurrentParseModel(ctx context.Context, content []byte, videoID string, batch bool) (providerMedia, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return providerMedia{}, errors.New("红果当前播放器取流返回格式异常")
	}
	for _, code := range []string{mapString(result, "code", "Code"), mapString(nestedMap(result, "BaseResp"), "StatusCode")} {
		if code != "" && code != "0" {
			return providerMedia{}, fmt.Errorf("红果当前播放器取流 code=%s", truncate(code, 20))
		}
	}
	data := nestedMap(result, "data")
	var model map[string]any
	if batch {
		model = nestedMap(data, videoID)
	} else {
		model = data
	}
	if len(model) == 0 {
		return providerMedia{}, errors.New("红果当前播放器取流未返回该集")
	}
	parsed, err := hongguoAppModel(model)
	if err != nil {
		return providerMedia{}, err
	}
	return downloader.hongguoMediaFromModel(ctx, parsed)
}

// resolveHongguoCurrentMedia —— 红果取流总入口。
// 现行协议是 V5 播放签名（provider_hongguo_playback_v5.go：preload/v1 → video_tab/v1）；
// 只有它整体被软拒绝时才回退下面的 field-25 旧方言，供上游回滚协议时兜底。
func (downloader *Downloader) resolveHongguoCurrentMedia(ctx context.Context, videoID string) (providerMedia, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return providerMedia{}, errors.New("红果视频 ID 无效")
	}
	media, v5Err := downloader.resolveHongguoPlaybackV5(ctx, videoID)
	if v5Err == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	legacyMedia, legacyErr := downloader.resolveHongguoLegacyPlayerMedia(ctx, videoID)
	if legacyErr == nil {
		return legacyMedia, nil
	}
	return providerMedia{}, fmt.Errorf("%v；field-25 旧方言：%v", publicError(v5Err), publicError(legacyErr))
}

// resolveHongguoLegacyPlayerMedia —— field 25 方言（2026-11-21 上线，11-23 起被上游整体软拒绝）。
// resolvePure → resolveCurrentBatch（AB-off/AB-on），空 body 时按 Swift resolveFresh 的顺序回退，
// 并在整体软拒绝后重铸一次设备身份。
func (downloader *Downloader) resolveHongguoLegacyPlayerMedia(ctx context.Context, videoID string) (providerMedia, error) {
	id, err := downloader.ensureHongguoPlayerIdentity(ctx)
	if err != nil {
		return providerMedia{}, err
	}
	pairs := hongguoCurrentPairs(id)

	batchPaths := []string{"/novel/player/multi_video_model/v1", "/novel/player/multi_video_model/player/v1"}
	var lastErr error
	for _, level := range []int{3, 4} {
		media, err := downloader.resolveHongguoCurrentPure(ctx, videoID, level, pairs)
		if err == nil {
			return media, nil
		}
		lastErr = err
		if !errors.Is(err, errHongguoPlayerEmptyBody) {
			continue
		}
		for _, path := range batchPaths {
			media, err := downloader.resolveHongguoCurrentBatch(ctx, videoID, level, pairs, path)
			if err == nil {
				return media, nil
			}
			lastErr = err
			if !errors.Is(err, errHongguoPlayerEmptyBody) {
				break
			}
		}
		if ctx.Err() != nil {
			return providerMedia{}, ctx.Err()
		}
	}
	// 全部软拒绝：重铸身份后再整体走一遍（resolveFresh 的 remint 语义）。
	if errors.Is(lastErr, errHongguoPlayerEmptyBody) {
		remintHongguoPlayerIdentity()
		if id2, err2 := downloader.ensureHongguoPlayerIdentity(ctx); err2 == nil && id2 != nil && id2.DeviceID != id.DeviceID {
			pairs = hongguoCurrentPairs(id2)
			for _, level := range []int{3, 4} {
				if media, err := downloader.resolveHongguoCurrentPure(ctx, videoID, level, pairs); err == nil {
					return media, nil
				}
				for _, path := range batchPaths {
					if media, err := downloader.resolveHongguoCurrentBatch(ctx, videoID, level, pairs, path); err == nil {
						return media, nil
					}
				}
			}
		}
	}
	if lastErr != nil {
		return providerMedia{}, lastErr
	}
	return providerMedia{}, errors.New("红果当前播放器取流失败")
}
