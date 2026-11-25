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

// 只走现行 V5 播放签名（preload/v1 → video_tab/v1），不落旧方言与网页兜底，
// 并连取多集，确认不是「某一条流恰好可用」。GZ_LIVE=1 才跑。
func TestZZLiveHongguoPlaybackV5Only(t *testing.T) {
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

	want := len(chapters)
	if want > 3 {
		want = 3
	}
	for index := 0; index < want; index++ {
		videoID := strings.TrimPrefix(chapters[index].VideoURL, "hongguo-cenc://")
		t0 := time.Now()
		media, err := engine.downloader.resolveHongguoPlaybackV5(ctx, videoID)
		if err != nil {
			t.Fatalf("第 %d 集 V5 取流失败（%.1fs）：%v", index+1, time.Since(t0).Seconds(), err)
		}
		if !isProviderHTTPMediaURL(media.URL) {
			t.Fatalf("第 %d 集返回了非法媒体地址：%q", index+1, media.URL)
		}
		t.Logf("第 %d 集 V5 成功（%.1fs）：%.90s", index+1, time.Since(t0).Seconds(), media.URL)
	}
}
