package core

import (
	"errors"
	"testing"
)

func TestHuangguoMergedCategorySplit(t *testing.T) {
	for category, want := range map[string]string{
		"ai:ai-duanju": sourceHuangguoAI,
		"video:5":      sourceHuangguoVideo,
		"old:hot":      sourceCloudFront,
	} {
		source, _, ok := splitHuangguoCategory(category)
		if !ok || source != want || !validHuangguoMergedCategory(category) {
			t.Errorf("%s → %s %v", category, source, ok)
		}
	}
	for _, bad := range []string{"", "ai-duanju", "ai:", "xx:1", "ai:not-a-category", "video:abc", "ai:../x"} {
		if validHuangguoMergedCategory(bad) {
			t.Errorf("应拒绝 %q", bad)
		}
	}
	if !validNativeCategory(sourceHuangguo, "") || !validNativeCategory(sourceHuangguo, "ai:ai-manju") {
		t.Fatal("聚合源的分类校验没接上")
	}
}

func TestMergeHuangguoListsInterleavesAndDedupes(t *testing.T) {
	d := func(id, title string) Drama { return Drama{ID: id, Title: title} }
	merged := mergeHuangguoLists([][]Drama{
		{d("huangguoai:1", "甲"), d("huangguoai:2", "乙"), d("huangguoai:3", "丙")},
		{d("cloudfront:a", "丁")},
		{d("huangguo-video:x", "乙"), d("huangguo-video:y", "戊")}, // 「乙」AI 也有，留 AI 那份
	})
	var got []string
	for _, item := range merged {
		got = append(got, item.ID)
	}
	want := []string{"huangguoai:1", "cloudfront:a", "huangguoai:2", "huangguo-video:y", "huangguoai:3"}
	if len(got) != len(want) {
		t.Fatalf("得到 %v，想要 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("得到 %v，想要 %v", got, want)
		}
	}
}

func TestHuangguoPartCircuitBreaker(t *testing.T) {
	const fake = "huangguo-test-part"
	if !huangguoPartUp(fake) {
		t.Fatal("初始应可用")
	}
	markHuangguoPart(fake, errors.New("连不上"))
	if huangguoPartUp(fake) {
		t.Fatal("失败后应熔断")
	}
	markHuangguoPart(fake, nil)
	if !huangguoPartUp(fake) {
		t.Fatal("成功后应恢复")
	}
}

func TestHuangguoAggregateIsListOnly(t *testing.T) {
	// 聚合源只用于列表；剧集 ID 里的 "huangguo:" 仍是黄果 AI 的旧别名
	if source, id, ok := splitProviderDramaID("huangguo:123"); !ok || source != sourceHuangguoAI || id != "123" {
		t.Fatalf("旧别名解析错：%s %s %v", source, id, ok)
	}
	if canonicalProviderSource("huangguo") != sourceHuangguo || !isHuangguoProviderSource(sourceHuangguo) {
		t.Fatal("聚合源没注册")
	}
	for _, removed := range []string{"yeguo", "dsd", "sorani", "huangdou"} {
		if isHuangguoProviderSource(removed) {
			t.Errorf("%s 应已删除", removed)
		}
		if _, _, ok := splitProviderDramaID(removed + ":1"); ok {
			t.Errorf("%s 的剧集 ID 不应再有效", removed)
		}
	}
}
