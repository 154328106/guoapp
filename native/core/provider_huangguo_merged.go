package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 黄果聚合源（2026-11-10 用户要求：黄果视频 / 黄果 AI / 黄果旧版 合成一个「黄果」）。
//
// 只聚合「列表」：首页 / 分类 / 本地搜索走 sourceHuangguo；每部剧的 ID 仍带原来那家的前缀
// （huangguoai: / huangguo-video: / cloudfront:），详情和取流照旧交给原来那家，Drama.Source 也保持原值。
//
//   - 首页：三家并发取同一页，按 AI → 旧版 → 视频 轮流穿插，按剧名去重（重复时留排前面那家）。
//   - 单家失败不影响整体；连不上的那家熔断 10 分钟（黄果视频国内被墙，不开代理时别每页都等它超时）。
//   - 分类：ID 带前缀 "ai:" / "old:" / "video:"，选了分类就只请求那一家。

const sourceHuangguo = "huangguo"

type huangguoPart struct {
	source  string
	prefix  string
	timeout time.Duration
}

// 顺序 = 合并时的优先级：AI 国内直连快；旧版直连可用；视频要代理，超时给短些。
// 旧版冷启动要先探测可用的 API 域名，第一次可能要十几秒，超时别给太短。
var huangguoParts = []huangguoPart{
	{source: sourceHuangguoAI, prefix: "ai", timeout: 20 * time.Second},
	{source: sourceCloudFront, prefix: "old", timeout: 20 * time.Second},
	{source: sourceHuangguoVideo, prefix: "video", timeout: 8 * time.Second},
}

const huangguoPartCooldown = 3 * time.Minute

var (
	huangguoPartMu   sync.Mutex
	huangguoPartDown = map[string]time.Time{}
)

func isHuangguoPart(source string) bool {
	for _, part := range huangguoParts {
		if part.source == source {
			return true
		}
	}
	return false
}

func huangguoPartUp(source string) bool {
	huangguoPartMu.Lock()
	defer huangguoPartMu.Unlock()
	return time.Now().After(huangguoPartDown[source])
}

func markHuangguoPart(source string, err error) {
	huangguoPartMu.Lock()
	defer huangguoPartMu.Unlock()
	if err == nil {
		delete(huangguoPartDown, source)
		return
	}
	huangguoPartDown[source] = time.Now().Add(huangguoPartCooldown)
}

// splitHuangguoCategory："ai:ai-duanju" → (huangguoai, "ai-duanju")。
func splitHuangguoCategory(category string) (string, string, bool) {
	prefix, rest, found := strings.Cut(category, ":")
	if !found || rest == "" {
		return "", "", false
	}
	for _, part := range huangguoParts {
		if part.prefix == prefix {
			return part.source, rest, true
		}
	}
	return "", "", false
}

func validHuangguoMergedCategory(category string) bool {
	source, inner, ok := splitHuangguoCategory(category)
	return ok && validNativeCategory(source, inner)
}

// fetchHuangguoPartPage：单家某一页（原来三个独立源各自的列表逻辑）。
func (d *Downloader) fetchHuangguoPartPage(ctx context.Context, source string, page int, category string) ([]Drama, bool, error) {
	switch source {
	case sourceHuangguoAI:
		return d.fetchHuangguoAICatalogPage(ctx, page, category)
	case sourceCloudFront:
		return d.fetchLegacyCatalogCategoryPage(ctx, page, category)
	case sourceHuangguoVideo:
		address := fmt.Sprintf("%s/videos?page=%d", d.providerBaseURL(source), page)
		if category != "" {
			address += "&category=" + url.QueryEscape(category)
		}
		body, err := d.fetchProviderText(ctx, address, d.providerBaseURL(source)+"/")
		if err != nil {
			return nil, false, err
		}
		items := parseHuangguoVideoCards(body, address)
		return items, len(items) >= 20, nil
	}
	return nil, false, errors.New("不是黄果的站源")
}

// fetchHuangguoMergedPage：聚合黄果的第 page 页；category 非空时只请求对应那一家。
func (d *Downloader) fetchHuangguoMergedPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	if category != "" {
		source, inner, ok := splitHuangguoCategory(category)
		if !ok {
			return nil, false, errors.New("内容分类无效")
		}
		items, more, err := d.fetchHuangguoPartPage(ctx, source, page, inner)
		markHuangguoPart(source, partNetworkError(ctx, err))
		return items, more, err
	}
	type partResult struct {
		items []Drama
		more  bool
		err   error
	}
	results := make([]partResult, len(huangguoParts))
	var group sync.WaitGroup
	for index, part := range huangguoParts {
		if !huangguoPartUp(part.source) {
			results[index].err = fmt.Errorf("%s 暂时不可用", part.source)
			continue
		}
		group.Add(1)
		go func(index int, part huangguoPart) {
			defer group.Done()
			partCtx, cancel := context.WithTimeout(ctx, part.timeout)
			defer cancel()
			items, more, err := d.fetchHuangguoPartPage(partCtx, part.source, page, "")
			markHuangguoPart(part.source, partNetworkError(ctx, err))
			results[index] = partResult{items: items, more: more, err: err}
		}(index, part)
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	lists := make([][]Drama, len(results))
	for index, result := range results {
		lists[index] = result.items
	}
	merged := mergeHuangguoLists(lists)
	more := false
	var failures []error
	for index, result := range results {
		more = more || result.more
		if result.err != nil {
			failures = append(failures, fmt.Errorf("%s：%w", huangguoParts[index].prefix, result.err))
			if huangguoPartUp(huangguoParts[index].source) {
				more = true // 这一家是临时出错，下一页还可能有
			}
		}
	}
	if len(merged) == 0 && len(failures) > 0 {
		return nil, false, errors.Join(failures...)
	}
	return merged, more, nil // 部分失败不报：黄果视频国内被墙，开代理才有，别每次都弹提示
}

// mergeHuangguoLists：轮流穿插（第 1 部 AI、第 1 部旧版、第 1 部视频、第 2 部 AI……），
// 按 ID 和剧名去重，重复时留排在前面的那家。
func mergeHuangguoLists(lists [][]Drama) []Drama {
	// 先定每个剧名归哪家：排在前面的那家（AI > 旧版 > 视频），不管它在自己列表里排第几
	owner := map[string]int{}
	for index := len(lists) - 1; index >= 0; index-- {
		for _, drama := range lists[index] {
			if title := hongguoSearchText(drama.DisplayTitle()); title != "" {
				owner[title] = index
			}
		}
	}
	var merged []Drama
	seenID, seenTitle := map[string]bool{}, map[string]bool{}
	for i := 0; ; i++ {
		any := false
		for index, list := range lists {
			if i >= len(list) {
				continue
			}
			any = true
			drama := list[i]
			title := hongguoSearchText(drama.DisplayTitle())
			if drama.ID == "" || seenID[drama.ID] || title != "" && (seenTitle[title] || owner[title] != index) {
				continue
			}
			seenID[drama.ID] = true
			if title != "" {
				seenTitle[title] = true
			}
			merged = append(merged, drama)
		}
		if !any {
			return merged
		}
	}
}

// partNetworkError：只有连不上才熔断。整体请求被取消、或只是这一家慢（撞到单家超时，
// 比如旧版冷启动探测域名）都不算——慢不等于坏，下一页还要给它机会。
func partNetworkError(parent context.Context, err error) error {
	if err == nil || parent.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

// nativeHuangguoCategories：三家的分类合在一起，ID 加前缀。
func (engine *nativeEngine) nativeHuangguoCategories(ctx context.Context, force bool) ([]nativeCategory, error) {
	engine.mu.Lock()
	cached := append([]nativeCategory{}, engine.categoryOptions[sourceHuangguo]...)
	engine.mu.Unlock()
	if len(cached) > 1 && !force {
		return cached, nil
	}
	type partCategories struct {
		list []nativeCategory
		err  error
	}
	results := make([]partCategories, len(huangguoParts))
	var group sync.WaitGroup
	for index, part := range huangguoParts {
		if !force && !huangguoPartUp(part.source) {
			results[index].err = errors.New("暂时不可用")
			continue
		}
		group.Add(1)
		go func(index int, part huangguoPart) {
			defer group.Done()
			partCtx, cancel := context.WithTimeout(ctx, part.timeout)
			defer cancel()
			list, err := engine.nativeCategories(partCtx, part.source, force)
			markHuangguoPart(part.source, partNetworkError(ctx, err))
			results[index] = partCategories{list: list, err: err}
		}(index, part)
	}
	group.Wait()
	all := []nativeCategory{{Name: "全部"}}
	seenName := map[string]bool{"全部": true}
	failed := false
	for index, result := range results {
		if result.err != nil {
			failed = true
			continue
		}
		for _, category := range result.list {
			if category.ID == "" || seenName[category.Name] {
				continue
			}
			seenName[category.Name] = true
			all = append(all, nativeCategory{ID: huangguoParts[index].prefix + ":" + category.ID, Name: category.Name})
		}
	}
	if len(all) == 1 {
		return nil, errors.New("黄果暂未返回内容分类，请稍后重试")
	}
	if !failed {
		engine.rememberCategories(sourceHuangguo, all) // 有一家没取到就不缓存，下次再补
	}
	return all, nil
}
