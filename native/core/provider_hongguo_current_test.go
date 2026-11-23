package core

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// 非联网自检：走一遍 HGCurrentPlayer.sign，确认各签名产物形态合法、内部无 panic/越界。
// 用于验证算法移植的字节级结构一致性（不校验线上网关是否接受）。
func TestHongguoCurrentSignSelfCheck(t *testing.T) {
	id := &hongguoPlayerIdentity{
		DeviceID:  "123456789012345",
		InstallID: "123456789016441",
		CDID:      "22daeba8-ef23-4742-abbe-c41bc3d6d4c8",
		OpenUDID:  "0011223344556677",
	}
	pairs := hongguoCurrentPairs(id)
	body := []byte(`{"biz_param":{"device_level":3,"need_all_video_definition":true,"video_id_type":0,"video_platform":3},"video_id":"7000000000000000000"}`)

	signed, err := hongguoCurrentSign(pairs, time.Now().UnixMilli(), id.DeviceID, hongguoCurrentVersionName, body)
	if err != nil {
		t.Fatalf("sign 失败：%v", err)
	}
	if signed.Query == "" {
		t.Fatal("query 为空")
	}
	if !strings.Contains(signed.Query, "_rticket=") || !strings.Contains(signed.Query, "ts=") {
		t.Fatalf("query 缺少 ts/_rticket：%s", signed.Query)
	}
	for _, key := range []string{"x-khronos", "x-argus", "x-ladon", "x-gorgon", "x-helios", "x-medusa", "x-ss-stub", "x-ss-req-ticket"} {
		if signed.Headers[key] == "" {
			t.Fatalf("缺少 header %s", key)
		}
	}
	if len(signed.Headers["x-ss-stub"]) != 32 {
		t.Fatalf("x-ss-stub 应为 32 位 hex，实际 %d", len(signed.Headers["x-ss-stub"]))
	}
	if _, err := hex.DecodeString(signed.Headers["x-ss-stub"]); err != nil {
		t.Fatalf("x-ss-stub 不是 hex：%v", err)
	}
	gorgon, err := hex.DecodeString(signed.Headers["x-gorgon"])
	if err != nil || len(gorgon) != 26 {
		t.Fatalf("x-gorgon 应为 26 字节 hex，实际 len=%d err=%v", len(gorgon), err)
	}
	if gorgon[0] != 0x84 || gorgon[1] != 0x04 {
		t.Fatalf("x-gorgon 头异常：%x", gorgon[:4])
	}
	if _, err := base64.StdEncoding.DecodeString(signed.Headers["x-helios"]); err != nil {
		t.Fatalf("x-helios 不是 base64：%v", err)
	}
	medusa, err := base64.StdEncoding.DecodeString(signed.Headers["x-medusa"])
	if err != nil || len(medusa) < 248 {
		t.Fatalf("x-medusa base64 异常：len=%d err=%v", len(medusa), err)
	}
	if _, err := base64.StdEncoding.DecodeString(signed.Headers["x-argus"]); err != nil {
		t.Fatalf("x-argus 不是 base64：%v", err)
	}
	if _, err := base64.StdEncoding.DecodeString(signed.Headers["x-ladon"]); err != nil {
		t.Fatalf("x-ladon 不是 base64：%v", err)
	}
}

// 覆盖 6 个 selector 分支，确保 factor 不越界且都产出 20 字节。
func TestHongguoCurrentFactorBranches(t *testing.T) {
	input := make([]byte, 52)
	for i := range input {
		input[i] = byte(i * 7)
	}
	for _, selector := range []int{0, 3, 6, 9, 12, 15} {
		out, err := hongguoCurrentFactor(input, 0x12345678, selector)
		if err != nil {
			t.Fatalf("selector=%d factor 失败：%v", selector, err)
		}
		if len(out) != 20 {
			t.Fatalf("selector=%d factor 应返回 20 字节，实际 %d", selector, len(out))
		}
	}
}

// 覆盖 transform 的 4 个 branch 与 mid 的往返解析。
func TestHongguoCurrentTransformAndMid(t *testing.T) {
	plain := make([]byte, 31)
	for i := range plain {
		plain[i] = byte(i)
	}
	key := make([]byte, 16)
	iv := make([]byte, 16)
	for i := 0; i < 16; i++ {
		key[i] = byte(0xa0 + i)
		iv[i] = byte(0x10 + i)
	}
	for branch := 0; branch < 4; branch++ {
		out, err := hongguoCurrentTransform(plain, key, iv, branch)
		if err != nil {
			t.Fatalf("branch=%d transform 失败：%v", branch, err)
		}
		if len(out) != 32 {
			t.Fatalf("branch=%d transform 应返回 32 字节，实际 %d", branch, len(out))
		}
	}

	pb := hongguoCurrentProtobuf(0x11223344, "123456789012345", "7.3.9.32", 0x55667788, make([]byte, 20), make([]byte, 6))
	mid, err := hongguoCurrentMid(pb)
	if err != nil {
		t.Fatalf("mid 失败：%v", err)
	}
	if mid != 0 {
		t.Fatalf("mid 应为 0（factor/hash 首字节为 0），实际 %d", mid)
	}
}
