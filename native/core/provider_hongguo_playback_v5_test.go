package core

import (
	"strings"
	"testing"
)

// 离线回归向量：与 iDuanJu STVTests/HGCurrentPlayerTests.testPlaybackV5GoldenVector 同一输入，
// 期望值取自上游 Swift 实现的输出。任何一处变换/常量被改错，这里立刻红。
func TestHongguoPlaybackV5GoldenVector(t *testing.T) {
	pairs := [][2]string{
		{"aid", "8662"}, {"app_name", "novelread"}, {"version_code", "74532"}, {"version_name", "7.4.5.32"},
		{"channel", "update_64"}, {"device_type", "25053RT47C"}, {"os_version", "16"},
		{"iid", "1000000000000002"}, {"cdid", "00000000-0000-4000-8000-000000000001"}, {"device_id", "1000000000000001"},
	}
	entropy := make([]byte, 16)
	for i := range entropy {
		entropy[i] = byte(i + 1)
	}
	field21 := 312
	signed, err := hongguoPlaybackV5SignWith(
		pairs, 1795500000123, []byte(`{"test":1}`), true, &field21,
		entropy, "fixedProcessNonce_000123", 1700000000000,
	)
	if err != nil {
		t.Fatalf("签名失败：%v", err)
	}

	wantQuery := "aid=8662&app_name=novelread&version_code=74532&version_name=7.4.5.32&channel=update_64" +
		"&device_type=25053RT47C&os_version=16&iid=1000000000000002" +
		"&cdid=00000000-0000-4000-8000-000000000001&device_id=1000000000000001&_rticket=1795500000150"
	if signed.Query != wantQuery {
		t.Fatalf("query 不一致\n得到：%s\n期望：%s", signed.Query, wantQuery)
	}
	if got := signed.Headers["x-khronos"]; got != "1795500000" {
		t.Fatalf("x-khronos 不一致：%s", got)
	}
	if got := signed.Headers["x-helios"]; got != "BQYHCBASuL65vfZGYSox1hnwyZB2YUGk7FjoLSo2Vo/XGzDK" {
		t.Fatalf("x-helios 不一致：%s", got)
	}
	const wantMedusa = "5ScFa81sSqGpUggo35KrR8JKyT0JCgABMootBg8UAf0DWL+Z40FS4c6If5a4H+wLyhAjkaSOe8V2FEY2SGCKlwmL+YJosK5cW72mO/hD6eHlyWO4o4EmOONZ/SKUpJ7TrWfi9KTjgndRtWFz0rpClBmfZC+f7hu1RqMhCvtT4AMPaKoe9fW3/VEoyWOHTRCK/s5uTbm0Qyv0gZARqU+9S2uBEHLTFHGFP/PsZr8PDWzWv2c6h3xGtfWZTbSa3de8S9aSPs0Z0rUNPcEMNHSO0t+G2fPEV0V0/nc7o0vZtXFDixR6j9io+h8402vfwu7Y8gNKqyOtN4T1QE8FxjlbD3X38iNMusJ3Pi3ZGIQBz6hVjmhIwm+exmTmj6t8ORR/gA6CndcJIqcpDq3kYBH7WqA8xzBNN1bFduUFn43Dtklvz4jhWPJo0zpGB9cHBX3Z8bKqWidiod226NGxOac+a4vh9Lyg3mYT6yw5ZKZ7Rdfo4OfFZv2kPgTPZ7s0iqoEmMMEsBNseX/DEtmssN3Ebd2IJosc3kBCF+JevSs6YPzUCwAKFZp7N5G7arc/0Es7r+GImcAlxk2JfcOKoSFhbikrGYaX1qYtWk69UZRiExQIqImvfmUVMytS/k/9VHX9FpmY/6f4//+n+P8LDA=="
	if got := signed.Headers["x-medusa"]; got != wantMedusa {
		t.Fatalf("x-medusa 不一致\n得到：%s\n期望：%s", got, wantMedusa)
	}
	if got := signed.Headers["x-ss-stub"]; got != "44E7A186DB2C24F55E9BF22E427DF2AD" {
		t.Fatalf("x-ss-stub 不一致：%s", got)
	}
	for _, name := range []string{"x-argus", "x-gorgon", "x-ladon"} {
		if _, ok := signed.Headers[name]; ok {
			t.Fatalf("V5 播放签名不应包含 %s", name)
		}
	}
	if !strings.HasPrefix(signed.Headers["x-medusa"], "5ScF") {
		t.Fatal("x-medusa 包头异常")
	}
}

// preload/v1 的请求体必须与上游一致（键排序后 JSON），并保持 gzip 可解。
func TestHongguoPreloadBodyShape(t *testing.T) {
	cfg := hongguoProtocolCurrent()
	if cfg.PreloadPath != "/novel/player/multi_video_model/preload/v1" || cfg.PreloadDrScene != "preload" || cfg.PreloadLevel != 2 {
		t.Fatalf("preload 配置异常：%+v", cfg)
	}
	if cfg.Field21 == nil || *cfg.Field21 != 312 {
		t.Fatalf("field21 应为 312，实际 %v", cfg.Field21)
	}
	if cfg.FallbackID.DeviceID == "" || cfg.FallbackID.InstallID == "" || cfg.FallbackID.CDID == "" {
		t.Fatal("基准设备身份缺失")
	}
	if cfg.TabLevel != 1 || len(cfg.TabHosts) == 0 || len(cfg.PreloadHosts) == 0 {
		t.Fatal("video_tab 兜底配置缺失")
	}
}
