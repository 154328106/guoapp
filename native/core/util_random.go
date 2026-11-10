package core

import (
	"crypto/rand"
	"encoding/hex"
)

// 随机串工具（原在 provider_huangdou.go，2026-11-10 删黄豆源时挪到这里；红果设备注册要用）。

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func uuidLike() string {
	raw := randomHex(16)
	if len(raw) != 32 {
		return raw
	}
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}
