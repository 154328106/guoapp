package core

// V5 取流签名（/novel/player/multi_video_model/player/v1）。
//
// 协议数据与变换结构逐行移植自 GPL-3.0-only 的 N3urda/hongguoTV（经 lihangyu97/novel-playlet
// 的 Rust 实现 v5.rs / v5_tables.rs / sm3.rs / ladon.rs 转写）。本项目已是 GPL，许可证兼容。
// 这些常量、S 盒、轮函数都是线上签名协议数据，改动任何一个字节都会导致网关丢弃请求返回空 body。

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"sync"
)

const (
	hongguoV5AppID       = 8662
	hongguoV5VersionCode = "74332"
	hongguoV5VersionName = "7.4.3.32"
	hongguoV5Channel     = "vivo_8662_64"
	hongguoV5DeviceType  = "PGT-AN10"
	hongguoV5OSVersion   = "12"
	hongguoV5License     = 646637794
	hongguoV5UserAgent   = "com.phoenix.read/73932 (Linux; U; Android 12; zh_CN_#Hans; PGT-AN10; Build/V417IR;tt-ok/3.12.13.20)"
)

var hongguoV5Magic = [16]byte{0x2d, 0x4b, 0x4f, 0xca, 0x49, 0x75, 0x0d, 0x43, 0x3f, 0xb5, 0xae, 0x2c, 0x22, 0x6d, 0xcc, 0x56}

var hongguoV5SignKey = [32]byte{
	0xb2, 0x3c, 0x2a, 0x47, 0x0a, 0x20, 0xd1, 0x05, 0xdc, 0x3f, 0x8c, 0x44, 0x38, 0xf8, 0x27, 0x2e,
	0xd4, 0x6d, 0x6c, 0x89, 0xff, 0xdc, 0xde, 0xdd, 0x51, 0x08, 0x69, 0x8d, 0x26, 0xe9, 0x48, 0x9d,
}

const hongguoV5SboxHex = "53290fe9e51f316f90f74a7e034d367784ab49237cd0135cdc632c322fd3be0e2624a417ecd9151a7ff64f60730a87f9445ec6ada2f865d22b696c4188f24c0df37b75510b939f6bc2fe6883e68bc8c722181ba0bdb08c9871b50c9a16b46eea8eed20ba4b8a14e0fcf1468dbbef05a93e5a9d7dcf06898635c1fd99f5de287a80c594daaab19b6279fbd75070cb52eb2555c456e39e7839853874e2f0013ac0b64742d8ca10e4b79c955de8b843ce4ed464a6a72ed508091c92e7b3eedd1e1dd12afac321618fa3d65f9167c982f43d66a53c6a1997585b346dbf9672ff37cdbc073b2dae11453304df00815476b2a13fccdb30b957a8acafe127400248591238922563e36406d37a24100b7902a028044e218453c2a399199c9d13dc40c55ba67e502273850ebdc1a2e2188b8f2d9e3cb909b8c46a2a9f4a6dd93007eafa65b7b3a881aa908cc74f0ad76bbbb58617c37012ca05a469fd44e62eecf1e4272ce860c0ae5eadf99b20d8f0234174006f3f110d7657d01645d4eff8f71471cbfe1cb2f52648727bf3c9de55153e031b4d61757db63dbf7782eeafe9018a8891362fb14759ac32bab03b349aeb5a377c5156be4c8dfbe13a684bda39cf836697c894545fe0f2491acea9fc6252f4ff1e0fe72bd2df6e67a7f635e5bcc6d1a5dd951d89960887a18eb40c785cdb1f80ed42299333ab98cdd67f316cd54346585dccd759fb722789b08807cc0fda293f7e5e1819bb0353517020b31d3d00374952bea2c421e862040a9ec892a05b266b7a0d0cc316f483b81af39715657ffe32ad8de0c13077646035236ee5363e5f900e9a7cf850a81cd9960b6df73317226f99eaa6fff15cec34acb98246639b918ab2851466a7cdfc4ad1b52c2ae95a58d3734e47ddd5b7560193f644ef9c408b7dee39b4d6aabf4de761e3252d4c7ba3843187745d55e6a5db05d42b45caed43c0c5d0b64bfa9d94a4a9d8f2f0e1a108dcfd288fd2bc1fc7bd675412138169cb09249875ba3b68cfb11180afae104fc9789571c6dfce02413886796ae21e3c488cc21b9f423a066c2eabf98e2fdeeb5776e4f5d09a515ba58cbbab3e37b09fd4f6d9244df80512e84cd7956be301cf64f1429d58577cc241f3a2c655144f81aaf95a4e108da894b38970561ae9002619fbbac91fa922ed202bfc62cb08e0de2a7f3631435f8b47c8a3c563b4db4071ce74c46da6fa696a9e282d1e825d663b93bfd56c48dce1884bb10fccea063929f746443fd830a4232e53da21a1f2d1bdfebc3a02c10a1b8ad31d04fd616590d272ac737e98eb33af60528716bee78ec0ee8699031177277d9cff32542c67caf5f44591681335ae0e178f3409a7803db83892dde5b9b7f0c750e24aef077badd678a09b850b84ec1597e4963cb5e675df1c0d6fb618496ecd76c35c798325590c2fb27a5e"

var hongguoV5HashTable = [64]uint32{0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8, 0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288, 0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e, 0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c, 0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3, 0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8, 0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14, 0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586}

var hongguoV5Sbox = func() [1024]byte {
	raw, err := hex.DecodeString(hongguoV5SboxHex)
	if err != nil || len(raw) != 1024 {
		panic("hongguo V5 sbox 常量损坏")
	}
	var out [1024]byte
	copy(out[:], raw)
	return out
}()

// 进程级随机 nonce 与启动时间（对应 Rust 的 OnceLock<ProcessState>）
var (
	hongguoV5ProcOnce  sync.Once
	hongguoV5Nonce     string
	hongguoV5StartedMs int64
)

func hongguoV5ProcessState(nowMs int64) (string, int64) {
	hongguoV5ProcOnce.Do(func() {
		var n [18]byte
		_, _ = rand.Read(n[:])
		hongguoV5Nonce = base64.RawURLEncoding.EncodeToString(n[:])
		hongguoV5StartedMs = nowMs
	})
	return hongguoV5Nonce, hongguoV5StartedMs
}

type hongguoV5Entropy struct {
	helios      uint32
	medusaNonce uint32
	packed      uint32
	message     uint32
}

func hongguoV5RandU32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}

func hongguoV5EntropyNew() hongguoV5Entropy {
	return hongguoV5Entropy{helios: hongguoV5RandU32(), medusaNonce: hongguoV5RandU32(), packed: hongguoV5RandU32(), message: hongguoV5RandU32()}
}

func le32(v uint32) [4]byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b
}

func md5Bytes(data []byte) [16]byte {
	return md5.Sum(data)
}

func rotr32(x uint32, k uint32) uint32 { return bits.RotateLeft32(x, -int(k)) }

// SM3 复用包内已有的标准实现 hongguoSM3（provider_hongguo_comment_sign.go）。

// ---------------- ladon (x-helios) ----------------

func ladonExpandHashTable(md5Hex string) [288]byte {
	var ht [288]byte
	bytesHex := []byte(md5Hex)
	prefixLen := 32
	if len(bytesHex) < prefixLen {
		prefixLen = len(bytesHex)
	}
	copy(ht[:prefixLen], bytesHex[:prefixLen])

	load := func(off int) uint64 {
		var vv uint64
		for j := 0; j < 8; j++ {
			vv |= uint64(ht[off+j]) << (j * 8)
		}
		return vv
	}
	temp := []uint64{load(0), load(8), load(16), load(24)}
	bufB0 := temp[0]
	bufB8 := temp[1]
	temp = temp[2:]

	for i := 0; i < 34; i++ {
		x9 := bufB0
		x8 := bufB8
		x8 = bits.RotateLeft64(x8, -8)
		x8 += x9
		x8 ^= uint64(i)
		temp = append(temp, x8)
		rorX9 := bits.RotateLeft64(x9, 3)
		x8 ^= rorX9
		for j := 0; j < 8; j++ {
			ht[(i+1)*8+j] = byte((x8 >> (j * 8)) & 255)
		}
		bufB0 = x8
		bufB8 = temp[0]
		temp = temp[1:]
	}
	return ht
}

func ladonEncryptBlock(ht *[288]byte, block []byte) [16]byte {
	var data0, data1 uint64
	for i := 0; i < 8; i++ {
		data0 |= uint64(block[i]) << (i * 8)
		data1 |= uint64(block[8+i]) << (i * 8)
	}
	for i := 0; i < 34; i++ {
		var hash uint64
		for j := 0; j < 8; j++ {
			hash |= uint64(ht[i*8+j]) << (j * 8)
		}
		rotated := bits.RotateLeft64(data1, -8)
		sum := data0 + rotated
		data1 = hash ^ sum
		rotated = bits.RotateLeft64(data0, 3)
		data0 = data1 ^ rotated
	}
	var out [16]byte
	for i := 0; i < 8; i++ {
		out[i] = byte((data0 >> (i * 8)) & 255)
		out[8+i] = byte((data1 >> (i * 8)) & 255)
	}
	return out
}

func ladonEncryptRaw(md5Hex string, data []byte) []byte {
	ht := ladonExpandHashTable(md5Hex)
	size := len(data)
	newSize := size + (16 - size%16)
	padVal := byte(newSize - size)
	input := make([]byte, newSize)
	for i := range input {
		input[i] = padVal
	}
	copy(input[:size], data)
	out := make([]byte, 0, newSize)
	for i := 0; i < newSize/16; i++ {
		block := ladonEncryptBlock(&ht, input[i*16:i*16+16])
		out = append(out, block[:]...)
	}
	return out
}

func ladonEncrypt(khronos, lcID, aid int64, randomBytes []byte) string {
	data := strconv.FormatInt(khronos, 10) + "-" + strconv.FormatInt(lcID, 10) + "-" + strconv.FormatInt(aid, 10)
	keygen := append([]byte(nil), randomBytes...)
	keygen = append(keygen, []byte(strconv.FormatInt(aid, 10))...)
	sum := md5.Sum(keygen)
	md5Hex := hex.EncodeToString(sum[:])
	encrypted := ladonEncryptRaw(md5Hex, []byte(data))
	out := append([]byte(nil), randomBytes...)
	out = append(out, encrypted...)
	return base64.StdEncoding.EncodeToString(out)
}

func hongguoV5Helios(ts, random uint32) string {
	r := le32(random)
	return ladonEncrypt(int64(ts), hongguoV5License, hongguoV5AppID, r[:])
}

// ---------------- branch / f13 ----------------

func hongguoGetIV(initial uint32, data []byte) uint32 {
	value := initial
	for index, b := range data {
		if index&1 == 0 {
			value = (value >> 4) ^ value ^ (value << 6) ^ uint32(b)
		} else {
			value = ^((value >> 7) ^ value ^ (uint32(b) | (value << 12)))
		}
	}
	return value
}

func hongguoBranchIV(queryHash []byte, bodyMD5 []byte, ts uint32) uint32 {
	step := hongguoGetIV(0x20230928, queryHash)
	step = hongguoGetIV(step, bodyMD5)
	t := le32(ts)
	return hongguoGetIV(step, t[:])
}

func hongguoSumMD5(data []byte) uint32 {
	check := uint32(0x20220420)
	for index := 0; index < 12; index++ {
		b := uint32(data[index])
		shift := uint(3)
		if index&1 != 0 {
			shift = 5
		}
		temp := (check >> shift) ^ check
		if index&1 == 0 {
			check = b ^ (check << 7)
		} else {
			check = ^(b | (check << 11))
		}
		check ^= temp
	}
	return (check | 4) ^ 0x01000000
}

func hongguoHashF13(queryHash []byte, bodyMD5 []byte, ts uint32) [20]byte {
	const seed = uint32(0x9d4a97c2)
	initialConst := [8]uint32{0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f, 984304912, 3005047866, 2874125293, 2152413264}

	count := seed + ts
	var table [64]uint32
	for i := 0; i < 64; i++ {
		table[i] = rotr32(hongguoV5HashTable[i], (count+1)&255)
	}

	shift := (count + 2) & 7
	var pad [4]byte
	for index, val := range [4]uint32{0xfa, 0x45, 0x61, 0xd7} {
		pad[index] = byte((val | (val << 8)) >> shift)
	}

	data := make([]byte, 0, 64)
	data = append(data, queryHash...)
	data = append(data, bodyMD5...)
	t := le32(ts)
	data = append(data, t[:]...)
	data = append(data, pad[:]...)
	data = append(data, 0, 0, 0, 0, 0, 0, 0x01, 0xa0)

	words := make([]uint32, 0, 128)
	for i := 0; i < len(data)/4; i++ {
		words = append(words, binary.BigEndian.Uint32(data[i*4:i*4+4]))
	}
	for index := 0; index < 112; index++ {
		x := words[index+1]
		y := words[index+14]
		value := words[index] + words[index+9] +
			(bits.RotateLeft32(x, 14) ^ bits.RotateLeft32(x, 25) ^ (x >> 3)) +
			(bits.RotateLeft32(y, 13) ^ bits.RotateLeft32(y, 15) ^ (y >> 10))
		words = append(words, value)
	}

	var initial [8]uint32
	for i := 0; i < 8; i++ {
		initial[i] = rotr32(initialConst[i], count)
	}
	state := initial
	for index := 0; index < 96; index++ {
		base := seed + uint32(index)
		n1 := ((state[4] ^ state[0]) & state[1]) ^ state[4]
		x := state[1]
		n2 := bits.RotateLeft32(x, 26) ^ bits.RotateLeft32(x, 21) ^ bits.RotateLeft32(x, 7)
		n4 := words[base&127] + n1 + n2 + table[base&63] + state[6]
		y := state[3]
		n5 := bits.RotateLeft32(y, 30) ^ bits.RotateLeft32(y, 19) ^ bits.RotateLeft32(y, 10)
		n6 := (y & state[5]) | ((y | state[5]) & state[2])
		old := state[7]
		// state.rotate_right(1): [s0..s7] -> [s7,s0,s1,s2,s3,s4,s5,s6]
		state = [8]uint32{state[7], state[0], state[1], state[2], state[3], state[4], state[5], state[6]}
		state[7] = n5 + n6 + n4
		state[0] = old + n4
	}

	var expanded [32]byte
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(expanded[i*4:i*4+4], state[i]+initial[i])
	}
	var folded [16]byte
	for i := 0; i < 16; i++ {
		folded[i] = expanded[i] ^ expanded[i+16]
	}
	var output [20]byte
	copy(output[:16], folded[:])
	tail := le32(hongguoSumMD5(folded[:]) | 0x20)
	copy(output[16:], tail[:])
	return output
}

// ---------------- xmxor ----------------

func hongguoXmxor(data []byte, key []byte) []byte {
	length := len(data)
	encoded := make([]byte, length)
	for index, value := range data {
		at := (index * 4) & 28
		d0 := uint32(key[at])
		d1 := uint32(key[at+1])
		v := uint32(value)
		mixed := ((((v << 4) | (v >> 4)) & 0xff) + d0) & 0xff
		mixed = (^mixed) ^ d1
		masked := mixed & 0xff
		mixed = ((((masked << 3) | (masked >> 5)) & 0xff) + d1) & 0xff
		mixed = (mixed ^ d0) & 0xff
		encoded[length-index-1] = byte(^mixed)
	}

	last := encoded[length-1] ^ encoded[length-2]
	first := encoded[0]
	encoded[0] = ^last + first
	encoded[1] = (encoded[0] ^ encoded[length-1] ^ 0xfe) + encoded[1]
	encoded[2] = encoded[2] + ((last - first) ^ bits.RotateLeft8(encoded[1], 3) ^ 2)

	for index := 0; index < length-4; index++ {
		temp := bits.RotateLeft8(encoded[index+2], 3) ^ encoded[index+1] ^ (byte(index) + 3)
		encoded[index+3] = ^temp + encoded[index+3]
	}

	encoded[length-1] ^= encoded[length-2]
	var total byte
	for _, v := range encoded[1:] {
		total += v
	}
	encoded[0] = (encoded[0] ^ encoded[1]) + total
	return encoded
}

// ---------------- AesV5 ----------------

type hongguoAesV5 struct {
	sbox  [256]byte
	con   [4]int
	order [16]int
	keys  [12][4]byte
}

func newHongguoAesV5(key [16]byte, ts uint32) hongguoAesV5 {
	initialSet := [4]uint32{0xbbb5de72, 0xd4638d5b, 0x92a5f73b, 0x89f8a1c9}
	conSet := [4][4]int{{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0}}
	orderSet := [4][16]int{
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7},
		{0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7},
		{0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7},
	}

	word := int(ts & 3)
	initial := initialSet[word]
	var sbox [256]byte
	copy(sbox[:], hongguoV5Sbox[word*256:(word+1)*256])

	var expanded [48]byte
	initialBytes := le32(initial)
	for offset := 0; offset < 4; offset++ {
		copy(expanded[offset*4:offset*4+4], initialBytes[:])
	}
	for i := 0; i < 16; i++ {
		expanded[i] ^= key[i]
	}

	rotation := uint32(8)
	for index := 4; index < 12; index++ {
		at := 4 * (index - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if index&3 == 0 {
			mixed := ((initial >> (rotation & 24)) ^ uint32(sbox[b])) & 0xff
			b = sbox[c]
			c = sbox[d]
			d = sbox[a]
			a = byte(mixed)
		}
		rotation += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}

	var keys [12][4]byte
	for i := 0; i < 12; i++ {
		copy(keys[i][:], expanded[i*4:i*4+4])
	}
	return hongguoAesV5{sbox: sbox, con: conSet[word], order: orderSet[word], keys: keys}
}

func (a *hongguoAesV5) addCon(rows *[4][4]byte, start int) {
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			rows[row][column] ^= a.keys[start+row][a.con[column]]
		}
	}
}

func (a *hongguoAesV5) block(value [16]byte) [16]byte {
	var rows [4][4]byte
	for row := 0; row < 4; row++ {
		copy(rows[row][:], value[row*4:row*4+4])
	}
	a.addCon(&rows, 0)

	for round := 1; round <= 2; round++ {
		for row := 0; row < 4; row++ {
			for column := 0; column < 4; column++ {
				rows[row][column] = a.sbox[rows[row][column]]
			}
		}
		previous := rows
		for row := 0; row < 4; row++ {
			rows[row] = previous[a.con[row]]
		}
		var flat [16]byte
		for row := 0; row < 4; row++ {
			copy(flat[row*4:row*4+4], rows[row][:])
		}
		for index := 0; index < 16; index++ {
			rows[index/4][index%4] = flat[a.order[index]]
		}
		if round == 1 {
			for row := 0; row < 4; row++ {
				prev := rows[row]
				for column := 0; column < 4; column++ {
					rows[row][column] = prev[a.con[column]]
				}
			}
			double := func(v byte) byte {
				r := v << 1
				if v&0x80 != 0 {
					r ^= 0x1b
				}
				return r
			}
			for column := 0; column < 4; column++ {
				sum := rows[0][column] ^ rows[1][column] ^ rows[2][column] ^ rows[3][column]
				first := rows[0][column]
				rows[0][column] ^= sum ^ double(rows[0][column]^rows[1][column])
				rows[1][column] ^= sum ^ double(rows[1][column]^rows[2][column])
				rows[2][column] ^= sum ^ double(rows[2][column]^rows[3][column])
				rows[3][column] ^= sum ^ double(rows[3][column]^first)
			}
		}
		a.addCon(&rows, round*4)
	}

	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			rows[row][column] ^= a.keys[4+row][column]
		}
	}
	var output [16]byte
	for row := 0; row < 4; row++ {
		copy(output[row*4:row*4+4], rows[row][:])
	}
	return output
}

func (a *hongguoAesV5) encrypt(source []byte, iv [16]byte) ([]byte, error) {
	wire := [8]uint{6, 5, 3, 4, 2, 7, 1, 0}
	compact := [8]uint{1, 7, 3, 6, 2, 0, 4, 5}
	if len(source) < 248 {
		return nil, errors.New("V5 AES 输入不足 248 字节")
	}
	var plain [32]byte
	for index := 0; index < 31; index++ {
		for position := 0; position < 8; position++ {
			bit := (source[index*8+position] >> wire[position]) & 1
			plain[index] |= bit << compact[position]
		}
	}
	plain[31] = 1

	var head [16]byte
	for i := 0; i < 16; i++ {
		head[i] = plain[i] ^ iv[i]
	}
	first := a.block(head)
	var tail [16]byte
	for i := 0; i < 16; i++ {
		tail[i] = plain[16+i] ^ first[i]
	}
	second := a.block(tail)

	var bitsArr [32]byte
	copy(bitsArr[:16], first[:])
	copy(bitsArr[16:], second[:])

	output := append([]byte(nil), source...)
	for index := 0; index < 31; index++ {
		for position := 0; position < 8; position++ {
			at := index*8 + position
			bit := (bitsArr[index] >> compact[position]) & 1
			output[at] = (output[at] &^ (1 << wire[position])) | (bit << wire[position])
		}
	}
	result := make([]byte, 0, len(output)+1)
	result = append(result, bitsArr[31])
	result = append(result, output...)
	return result, nil
}

// ---------------- protobuf ----------------

func hongguoProtoVarint(value uint64, out *[]byte) {
	for value > 0x7f {
		*out = append(*out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	*out = append(*out, byte(value))
}

func hongguoProtoZigzag(value int64) uint64 {
	if value < 0 {
		return uint64(-value)*2 - 1
	}
	return uint64(value) * 2
}

type hongguoProtoField struct {
	tag   uint32
	isInt bool
	sint  int64
	bytes []byte
}

func hongguoProtoSint(tag uint32, v int64) hongguoProtoField {
	return hongguoProtoField{tag: tag, isInt: true, sint: v}
}

func hongguoProtoBytes(tag uint32, v []byte) hongguoProtoField {
	return hongguoProtoField{tag: tag, bytes: v}
}

func hongguoProtoString(tag uint32, v string) hongguoProtoField {
	return hongguoProtoField{tag: tag, bytes: []byte(v)}
}

func hongguoProto(fields []hongguoProtoField) []byte {
	var out []byte
	for _, f := range fields {
		if f.isInt {
			hongguoProtoVarint(uint64(f.tag)<<3, &out)
			hongguoProtoVarint(hongguoProtoZigzag(f.sint), &out)
		} else {
			if len(f.bytes) == 0 {
				continue
			}
			hongguoProtoVarint((uint64(f.tag)<<3)|2, &out)
			hongguoProtoVarint(uint64(len(f.bytes)), &out)
			out = append(out, f.bytes...)
		}
	}
	return out
}

// ---------------- medusa ----------------

func hongguoMedusa(queryHash []byte, bodyMD5 []byte, ts uint32, deviceID string, nonce string, startedMs int64, entropy hongguoV5Entropy) (string, error) {
	appID := strconv.Itoa(hongguoV5AppID)
	license := strconv.Itoa(hongguoV5License)
	f13 := hongguoHashF13(queryHash, bodyMD5, ts)
	var flags [8]byte

	device := hongguoProto([]hongguoProtoField{
		hongguoProtoSint(1, 1),
		hongguoProtoSint(2, 2),
		hongguoProtoString(3, appID),
		hongguoProtoString(4, deviceID),
		hongguoProtoString(5, nonce),
		hongguoProtoString(6, "!noperm!"),
		hongguoProtoString(12, "Asia/Shanghai,8"),
		hongguoProtoString(13, "zh_CN"),
		hongguoProtoString(22, hongguoV5OSVersion),
		hongguoProtoSint(25, startedMs),
		hongguoProtoString(30, hongguoV5DeviceType),
		hongguoProtoString(31, "HONOR"),
		hongguoProtoString(32, hongguoV5DeviceType),
		hongguoProtoString(34, "HONOR"),
	})

	environment := hongguoProto([]hongguoProtoField{
		hongguoProtoString(6, "v04.10.03.04-bugfix"),
		hongguoProtoString(8, hongguoV5Channel),
		hongguoProtoBytes(12, device),
		hongguoProtoString(14, hongguoV5VersionName),
	})

	nested15 := hongguoProto([]hongguoProtoField{
		hongguoProtoSint(1, 1),
		hongguoProtoSint(2, 1),
		hongguoProtoSint(3, 1),
		hongguoProtoSint(5, 1),
	})

	message := hongguoProto([]hongguoProtoField{
		hongguoProtoBytes(1, hongguoV5Magic[:]),
		hongguoProtoSint(2, 5),
		hongguoProtoSint(3, int64(entropy.message)),
		hongguoProtoString(4, appID),
		hongguoProtoString(5, deviceID),
		hongguoProtoString(6, license),
		hongguoProtoString(7, hongguoV5VersionName),
		hongguoProtoString(8, "v04.10.03-ml-android"),
		hongguoProtoSint(9, 0x040a0300),
		hongguoProtoBytes(10, flags[:]),
		hongguoProtoSint(12, int64(ts)),
		hongguoProtoBytes(13, f13[:]),
		hongguoProtoBytes(14, queryHash[:6]),
		hongguoProtoBytes(15, nested15),
		hongguoProtoSint(17, int64(ts)),
		hongguoProtoString(20, "none"),
		hongguoProtoSint(21, 377),
		hongguoProtoBytes(23, environment),
		hongguoProtoString(24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`),
	})

	seedInput := make([]byte, 0, 68)
	seedInput = append(seedInput, hongguoV5SignKey[:]...)
	mn := le32(entropy.medusaNonce)
	seedInput = append(seedInput, mn[:]...)
	seedInput = append(seedInput, hongguoV5SignKey[:]...)
	seedHash := hongguoSM3(seedInput)

	high := (entropy.medusaNonce >> 16) & 0xff
	seedWord := ((high << 11) | (entropy.medusaNonce >> 24)) ^ (high >> 5) ^ high
	seed := le32(^seedWord)

	transformed := append([]byte(nil), flags[:]...)
	transformed = append(transformed, hongguoXmxor(message, seedHash[:])...)
	// reverse
	for i, j := 0, len(transformed)-1; i < j; i, j = i+1, j-1 {
		transformed[i], transformed[j] = transformed[j], transformed[i]
	}
	for index := range transformed {
		transformed[index] ^= seed[3-(index&3)]
	}

	check := uint32(0x18000001) | ((uint32(queryHash[0]) & 63) << 14) | ((uint32(f13[0]) & 63) << 8)
	packed := make([]byte, 0, 11+len(transformed))
	packed = append(packed, 0xca)
	ep := le32(entropy.packed)
	packed = append(packed, ep[:]...)
	ck := le32(check)
	packed = append(packed, ck[:]...)
	packed = append(packed, transformed...)
	packed = append(packed, byte(entropy.medusaNonce>>16))
	packed = append(packed, byte(entropy.medusaNonce>>24))

	key := md5Bytes(hongguoV5SignKey[:16])
	iv := md5Bytes(hongguoV5SignKey[16:])
	aes := newHongguoAesV5(key, ts)
	encrypted, err := aes.encrypt(packed, iv)
	if err != nil {
		return "", err
	}

	prefix := make([]byte, 0, 20)
	p5 := le32(5)
	prefix = append(prefix, p5[:]...)
	prefix = append(prefix, hongguoV5Magic[:]...)
	timestamp := le32(ts)
	for index := range prefix {
		prefix[index] ^= timestamp[index&3]
	}

	output := prefix
	output = append(output, byte(entropy.medusaNonce))
	output = append(output, byte(entropy.medusaNonce>>8))
	output = append(output, 0)
	output = append(output, 1)
	output = append(output, encrypted...)
	return base64.StdEncoding.EncodeToString(output), nil
}

// ---------------- query / sign ----------------

func hongguoV5EncodeComponent(value string) string {
	var sb strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '*' || b == '~' {
			sb.WriteByte(b)
		} else {
			sb.WriteString("%")
			sb.WriteString(strings.ToUpper(hex.EncodeToString([]byte{b})))
		}
	}
	return sb.String()
}

func hongguoV5PlaybackQuery(deviceID, installID, cdid string, ticket int64, appID string) string {
	pairs := [][2]string{
		{"aid", appID},
		{"app_name", "novelread"},
		{"version_code", hongguoV5VersionCode},
		{"version_name", hongguoV5VersionName},
		{"channel", hongguoV5Channel},
		{"device_platform", "android"},
		{"device_type", hongguoV5DeviceType},
		{"os_version", hongguoV5OSVersion},
		{"update_version_code", hongguoV5VersionCode},
		{"iid", installID},
		{"cdid", cdid},
		{"device_id", deviceID},
	}
	var sb strings.Builder
	for i, p := range pairs {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(hongguoV5EncodeComponent(p[0]))
		sb.WriteByte('=')
		sb.WriteString(hongguoV5EncodeComponent(p[1]))
	}
	sb.WriteString("&_rticket=")
	sb.WriteString(strconv.FormatInt(ticket, 10))
	return sb.String()
}

// signHongguoV5Playback 为 /novel/player/multi_video_model/player/v1 签名。
// compressedBody 必须是实际发送的 gzip 字节。返回最终 URL 与要设置的请求头。
func signHongguoV5Playback(base string, compressedBody []byte, deviceID, installID, cdid string, nowMs int64) (string, [][2]string, error) {
	if base == "" {
		return "", nil, errors.New("V5 取流 base URL 为空")
	}
	if strings.ContainsAny(base, "?#") {
		return "", nil, errors.New("V5 取流 base URL 不能带 query 或 fragment")
	}
	if deviceID == "" || installID == "" || cdid == "" {
		return "", nil, errors.New("V5 取流设备身份不完整")
	}
	ts := uint32(nowMs / 1000)
	bodyMD5 := md5Bytes(compressedBody)
	nonce, startedMs := hongguoV5ProcessState(nowMs)
	appID := strconv.Itoa(hongguoV5AppID)

	for offset := int64(0); offset < 1024; offset++ {
		ticket := nowMs + offset
		query := hongguoV5PlaybackQuery(deviceID, installID, cdid, ticket, appID)
		queryHash := hongguoSM3([]byte(query))
		if hongguoBranchIV(queryHash[:], bodyMD5[:], ts)&15 != 12 {
			continue
		}
		entropy := hongguoV5EntropyNew()
		medusa, err := hongguoMedusa(queryHash[:], bodyMD5[:], ts, deviceID, nonce, startedMs, entropy)
		if err != nil {
			return "", nil, err
		}
		url := base + "?" + query
		headers := [][2]string{
			{"x-khronos", strconv.FormatUint(uint64(ts), 10)},
			{"x-helios", hongguoV5Helios(ts, entropy.helios)},
			{"x-medusa", medusa},
			{"User-Agent", hongguoV5UserAgent},
			{"Accept", "application/json"},
			{"Content-Type", "application/json; charset=utf-8"},
			{"Content-Encoding", "gzip"},
			{"x-ss-stub", strings.ToUpper(hex.EncodeToString(bodyMD5[:]))},
		}
		return url, headers, nil
	}
	return "", nil, errors.New("V5 取流签名未找到 branch-12 的 ticket")
}
