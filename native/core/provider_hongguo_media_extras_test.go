package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"
)

// 按 app-source.js 的格式造一个 key_seed 包裹的地址：base64(a8 xx 01 00 ‖ AES-128-CBC(PKCS7(url)))
func encryptSpadeURLForTest(t *testing.T, address string, seed []byte) string {
	t.Helper()
	h1 := sha512.Sum512(seed)
	h2 := sha512.Sum512(append(h1[:], hongguoSpadeURLConstant...))
	block, err := aes.NewCipher(h2[:16])
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(address)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, []byte(strings.Repeat(string(rune(pad)), pad))...)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, h2[16:32]).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(append([]byte{0xa8, 0x37, 0x01, 0x00}, out...))
}

func TestDecryptHongguoSpadeURLRoundTrip(t *testing.T) {
	seed := []byte("0123456789abcdef-seed")
	want := "https://v3-novel.qznovelvod.com/abc/def.mp4?a=1&b=2"
	enc := encryptSpadeURLForTest(t, want, seed)
	got, ok := decryptHongguoSpadeURL(enc, seed)
	if !ok || got != want {
		t.Fatalf("解密 = %q, %v；想要 %q", got, ok, want)
	}
	// url-safe、去掉填充的写法也要认
	safe := strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(enc), "=")
	if got, ok := decryptHongguoSpadeURL(safe, seed); !ok || got != want {
		t.Fatalf("url-safe 解密 = %q, %v", got, ok)
	}
	if _, ok := decryptHongguoSpadeURL(enc, []byte("wrong seed")); ok {
		t.Fatal("错误的 seed 不应解出合法地址")
	}
	if _, ok := decryptHongguoSpadeURL(base64.StdEncoding.EncodeToString([]byte("plain text, no header")), seed); ok {
		t.Fatal("没有 a8..0100 头的不应当成加密地址")
	}
	evil := encryptSpadeURLForTest(t, "https://evil.example.com/x.mp4", seed)
	if _, ok := decryptHongguoSpadeURL(evil, seed); ok {
		t.Fatal("非红果视频 CDN 的地址应被拒绝")
	}
}

func TestSelectHongguoAppMediaWithKeySeed(t *testing.T) {
	seed := []byte("seed-for-select")
	main := "https://v26-novel.fqnovelvod.com/video/main.mp4"
	backup := "https://v9-novel.douyinvod.com/video/backup.mp4"
	model := map[string]any{
		"key_seed":       base64.RawURLEncoding.EncodeToString(seed),
		"video_duration": "60",
		"video_list": []any{map[string]any{
			"video_meta":      map[string]any{"codec_type": "h264", "definition": "720p"},
			"main_url":        encryptSpadeURLForTest(t, main, seed),
			"backup_url_list": []any{encryptSpadeURLForTest(t, backup, seed)},
		}},
	}
	media, err := selectHongguoAppMedia(model)
	if err != nil {
		t.Fatal(err)
	}
	if media.URL != main || media.Quality != 720 {
		t.Fatalf("选中 %q（%dp），想要 %q", media.URL, media.Quality, main)
	}
	if len(media.Variants) != 2 || media.Variants[1].URL != backup {
		t.Fatalf("备用地址没解出来：%+v", media.Variants)
	}
}

func TestHongguoFallbackAPIShapes(t *testing.T) {
	const want = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/fallback?vid=1"
	for name, value := range map[string]any{
		"字符串":      want,
		"JSON 字符串": `{"fallback_api":"` + want + `"}`,
		"数组":       []any{want},
		"对象":       map[string]any{"fallback_api": want},
	} {
		if got := hongguoFallbackAPI(value); got != want {
			t.Errorf("%s：得到 %q", name, got)
		}
	}
	if hongguoHasInlineList(map[string]any{"fallback_api": want}) {
		t.Fatal("没有 video_list 不应算有内联列表")
	}
	d := &Downloader{}
	if _, err := d.fetchHongguoFallbackModel(context.Background(), "https://evil.example.com/x"); err == nil || !strings.Contains(err.Error(), "不受信任") {
		t.Fatalf("不可信域名应被拒：%v", err)
	}
}
