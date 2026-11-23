package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 真实联网回归：直接调新路径 resolveHongguoCurrentMedia（不经过 resolveHongguoMedia 的兜底链），
// 确认 field-25 新方言自己能解出片源，而非落入 Web/备用兜底。GZ_LIVE=1 才跑。
func TestZZLiveHongguoCurrentPathOnly(t *testing.T) {
	if os.Getenv("GZ_LIVE") != "1" {
		t.Skip("设 GZ_LIVE=1 才跑真实联网测试")
	}
	buildAllSources = "true"
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	catalog, err := engine.nativeCatalog(ctx, nativeInput{Source: sourceHongguo, Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Items) == 0 {
		t.Fatal("首页空")
	}
	raw, err := engine.nativeDetail(ctx, catalog.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	chapters := raw.(map[string]any)["chapters"].([]Chapter)
	if len(chapters) == 0 {
		t.Fatal("无章节")
	}
	videoID := strings.TrimPrefix(chapters[0].VideoURL, "hongguo-cenc://")
	t.Logf("剧：%s，videoID=%s", catalog.Items[0].Title, videoID)

	t0 := time.Now()
	media, err := engine.downloader.resolveHongguoCurrentMedia(ctx, videoID)
	if err != nil {
		t.Fatalf("新路径失败（%.1fs）：%v", time.Since(t0).Seconds(), err)
	}
	if !isProviderHTTPMediaURL(media.URL) {
		t.Fatalf("新路径返回了非法媒体地址：%q", media.URL)
	}
	t.Logf("新路径成功（%.1fs）：%.100s", time.Since(t0).Seconds(), media.URL)
}
