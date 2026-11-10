package core

// 真实联网冒烟测试：GZ_LIVE=1 才跑（CI 和平时都跳过）。
// GZ_LIVE_SOURCE 指定源（默认 huangguoai），走一遍 首页 → 详情 → 第 1 集取流。

import (
	"context"
	"os"
	"testing"
	"time"
)

// 黄果三个榜单（GZ_LIVE=1 才跑）
func TestZZLiveHuangguoRankings(t *testing.T) {
	if os.Getenv("GZ_LIVE") != "1" {
		t.Skip("设 GZ_LIVE=1 才跑真实联网测试")
	}
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, id := range []string{"huangguo-hot", "huangguo-recommend", "huangguo-potential"} {
		board, _ := findRankingBoard(id)
		t0 := time.Now()
		page, err := engine.downloader.fetchRankingPage(ctx, board, 1)
		if err != nil {
			t.Errorf("%s 失败（%.1fs）：%v", board.Name, time.Since(t0).Seconds(), err)
			continue
		}
		first := ""
		if len(page.Items) > 0 {
			first = page.Items[0].Drama.Title
		}
		t.Logf("%s %.1fs：%d 部，第 1 名《%s》", board.Name, time.Since(t0).Seconds(), len(page.Items), first)
	}
}

func TestZZLiveSourceSmoke(t *testing.T) {
	if os.Getenv("GZ_LIVE") != "1" {
		t.Skip("设 GZ_LIVE=1 才跑真实联网测试")
	}
	buildAllSources = "true"
	source := os.Getenv("GZ_LIVE_SOURCE")
	if source == "" {
		source = sourceHuangguoAI
	}
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tc := time.Now()
	if categories, err := engine.nativeCategories(ctx, source, false); err != nil {
		t.Logf("分类失败（%.1fs）：%v", time.Since(tc).Seconds(), err)
	} else {
		names := make([]string, 0, len(categories))
		for _, category := range categories {
			names = append(names, category.Name+"("+category.ID+")")
		}
		t.Logf("分类 %.1fs：%d 个 %v", time.Since(tc).Seconds(), len(categories), names)
	}

	t0 := time.Now()
	catalog, err := engine.nativeCatalog(ctx, nativeInput{Source: source, Page: 1, Query: os.Getenv("GZ_LIVE_QUERY")})
	if err != nil {
		t.Fatalf("首页/搜索失败（%.1fs）：%v", time.Since(t0).Seconds(), err)
	}
	t.Logf("首页/搜索 %.1fs：%d 部，hasMore=%v", time.Since(t0).Seconds(), len(catalog.Items), catalog.HasMore)
	if len(catalog.Items) == 0 {
		t.Fatal("首页是空的")
	}
	for i, d := range catalog.Items {
		if i < 3 {
			t.Logf("  %s 《%s》 %d 集", d.ID, d.Title, d.Episodes)
		}
	}

	t1 := time.Now()
	raw, err := engine.nativeDetail(ctx, catalog.Items[0])
	if err != nil {
		t.Fatalf("详情失败（%.1fs）：%v", time.Since(t1).Seconds(), err)
	}
	detail := raw.(map[string]any)
	chapters := detail["chapters"].([]Chapter)
	t.Logf("详情 %.1fs：《%s》%d 集", time.Since(t1).Seconds(), detail["drama"].(nativeDrama).Title, len(chapters))

	t2 := time.Now()
	media, err := engine.downloader.resolveProviderMedia(ctx, Task{DramaID: catalog.Items[0].ID, DramaTitle: catalog.Items[0].Title, Chapter: chapters[0], Index: 0})
	if err != nil {
		t.Fatalf("第 1 集取流失败（%.1fs）：%v", time.Since(t2).Seconds(), err)
	}
	t.Logf("第 1 集取流 %.1fs：%.100s", time.Since(t2).Seconds(), media.URL)
}
