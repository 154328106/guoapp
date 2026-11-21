package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// V5 取流端点：host 从 sinfonlineb 换到 sinfonlinea，路径尾部多了 /player/v1。
const hongguoV5PlaybackURL = "https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1"

func hongguoV5RequestBody(videoID string) ([]byte, error) {
	payload := map[string]any{
		"biz_param": map[string]any{
			"detail_page_version":       0,
			"device_level":              1,
			"disable_digg_stat":         false,
			"disable_video_relate_book": false,
			"need_all_video_definition": true,
			"need_mp4_align":            false,
			"use_os_player":             false,
			"use_server_dns":            false,
			"video_platform":            1024,
		},
		"mixed_video_id_map": map[string][]string{"1004": {videoID}},
		"dr_scene":           "player",
	}
	return json.Marshal(payload)
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hongguoV5Fetch 发一次 V5 签名取流请求，返回解析后的 JSON map。
func (downloader *Downloader) hongguoV5Fetch(ctx context.Context, videoID string) (map[string]any, error) {
	if !hongguoNumericID.MatchString(videoID) {
		return nil, errors.New("红果视频 ID 无效")
	}
	if err := downloader.ensureHongguoDevice(ctx, false, ""); err != nil {
		return nil, err
	}
	client := downloader.hongguoClient()
	client.mu.Lock()
	deviceID, installID, registered := client.deviceLocked(false)
	if client.state.CDID == "" {
		client.state.CDID = uuidLike()
	}
	cdid := client.state.CDID
	client.mu.Unlock()
	if !registered || deviceID == "" || installID == "" {
		return nil, errors.New("红果设备未就绪")
	}

	raw, err := hongguoV5RequestBody(videoID)
	if err != nil {
		return nil, err
	}
	compressed, err := gzipBytes(raw)
	if err != nil {
		return nil, err
	}

	url, headers, err := signHongguoV5Playback(hongguoV5PlaybackURL, compressed, deviceID, installID, cdid, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	for _, kv := range headers {
		request.Header.Set(kv[0], kv[1])
	}

	response, err := downloader.doPreparedCatalogRequest(request, 20*time.Second, nil)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("红果 V5 取流 HTTP %d", response.StatusCode)
	}
	if len(content) == 0 {
		return nil, errors.New("红果 V5 取流返回空响应（签名可能未被接受）")
	}
	if len(content) > providerMaxBodyBytes {
		return nil, errors.New("红果 V5 取流响应过大")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("红果 V5 取流返回格式异常")
	}
	return result, nil
}

// resolveHongguoV5Media 通过 V5 端点取该集的可播放媒体。
func (downloader *Downloader) resolveHongguoV5Media(ctx context.Context, videoID string) (providerMedia, error) {
	result, err := downloader.hongguoV5Fetch(ctx, videoID)
	if err != nil {
		return providerMedia{}, err
	}
	data := nestedMap(nestedMap(result, "data"), videoID)
	if len(data) == 0 {
		// 有些返回把单集直接放在 data 下
		data = nestedMap(result, "data")
	}
	if len(data) == 0 {
		return providerMedia{}, errors.New("红果 V5 取流未返回该集")
	}
	model, err := hongguoAppModel(data)
	if err != nil {
		return providerMedia{}, err
	}
	return downloader.hongguoMediaFromModel(ctx, model)
}
