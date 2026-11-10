package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func providerParityDownloader(t *testing.T, transport func(*http.Request, url.Values) (*http.Response, error)) *Downloader {
	t.Helper()
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		form := request.URL.Query()
		if request.Body != nil {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			bodyForm, _ := url.ParseQuery(string(body))
			for key, values := range bodyForm {
				form[key] = append(form[key], values...)
			}
		}
		return transport(request, form)
	})
	d.cfg.HuangjuAPIURL = "https://api.huangju.test"
	d.cfg.HuangjuURL = "https://huangju.test"
	return d
}

func TestProviderSourceParsesTextFixturesAndRejectsMismatchedIdentity(t *testing.T) {
	huangjuRow := map[string]any{"id": "77", "slug": "sample-drama", "title": "剧果样本", "totalEpisodes": json.Number("2"), "status": "completed", "categories": []any{"热播"}, "year": "2026", "score": "8.5"}
	drama, err := huangjuDramaFromMap(huangjuRow, huangjuBaseURL, "")
	if err != nil || drama.ID != "huangju:sample-drama-77" || drama.ReleaseStatus != "finished" || drama.EpisodeCount != 2 || drama.ChannelName != "剧果" {
		t.Fatalf("huangju text row parsed incorrectly: %+v %v", drama, err)
	}
	if _, err := huangjuDramaFromMap(huangjuRow, huangjuBaseURL, "sample-drama-78"); err == nil {
		t.Fatal("huangju accepted a mismatched source id")
	}
	if validHuangjuID("bad:value") || !validHuangjuID(strings.Repeat("a", 450)) || validHuangjuID(strings.Repeat("a", 451)) {
		t.Fatal("huangju id validation changed")
	}
}

func TestHuangjuPlaybackUsesSignedCookiesForPlaylist(t *testing.T) {
	var sawCookie bool
	var mediaURL string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/auth/guest":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"token":"guest-token"}`)
		case "/play/episode-1":
			response.Header().Add("Set-Cookie", "CloudFront-Policy=policy; Path=/; Max-Age=3600")
			response.Header().Add("Set-Cookie", "CloudFront-Signature=signature; Path=/; Max-Age=3600")
			response.Header().Add("Set-Cookie", "CloudFront-Key-Pair-Id=keypair; Path=/; Max-Age=3600")
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"url":`+strconv.Quote(mediaURL)+`,"expiresAt":`+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}`)
		case "/media.m3u8":
			sawCookie = strings.Contains(request.Header.Get("Cookie"), "CloudFront-Policy=policy") &&
				strings.Contains(request.Header.Get("Cookie"), "CloudFront-Signature=signature") &&
				strings.Contains(request.Header.Get("Cookie"), "CloudFront-Key-Pair-Id=keypair")
			if !sawCookie {
				t.Fatalf("missing signed media cookies: %s", request.Header.Get("Cookie"))
			}
			response.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(response, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST\n")
		default:
			t.Fatalf("unexpected route: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	mediaURL = server.URL + "/media.m3u8"
	d := &Downloader{cfg: Config{HuangjuAPIURL: server.URL, HuangjuURL: "https://huangju.test"}, client: server.Client(), limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	media, err := d.resolveHuangjuMedia(context.Background(), Task{DramaID: providerDramaID(sourceHuangju, "sample-77"), Chapter: Chapter{ID: providerChapterID(sourceHuangju, "sample-77", "episode-1")}})
	if err != nil || !sawCookie || media.Playlist == "" || !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Fatalf("huangju playback was not resolved through signed playlist: %+v %v", media, err)
	}
}

func TestProviderMediaCredentialsLimitOriginAndUseBackupAddresses(t *testing.T) {
	primary := "https://media.example.test/main.mp4"
	backup := "https://media.example.test/backup.mp4"
	model := map[string]any{"video_duration": "6", "video_list": []any{map[string]any{
		"main_url": "invalid", "backup_url": base64.StdEncoding.EncodeToString([]byte(primary)), "backup_urls": []any{backup},
		"video_meta": map[string]any{"codec_type": "h264", "definition": "720p"},
	}}}
	media, err := selectHongguoAppMedia(model)
	if err != nil || media.URL != primary || len(media.Variants) != 2 || media.Variants[1].URL != backup || media.Duration != 6*time.Second {
		t.Fatalf("hongguo backup media fields were lost: %+v %v", media, err)
	}
	credentials := &providerMediaCredentials{origin: "https://media.example.test", cookie: "Signed=fixture", referer: "https://source.example.test/page", userAgent: "Agent"}
	request, _ := http.NewRequest(http.MethodGet, "https://media.example.test/part", nil)
	if err := credentials.apply(request); err != nil || request.Header.Get("Cookie") != "Signed=fixture" || request.Header.Get("Origin") != "https://source.example.test" || request.Header.Get("User-Agent") != "Agent" {
		t.Fatal("credentials not applied on exact origin", err, request.Header)
	}
	other, _ := http.NewRequest(http.MethodGet, "https://media.example.test:444/part", nil)
	other.Header.Set("Cookie", "old=value")
	if err := credentials.apply(other); err != nil || other.Header.Get("Cookie") != "" {
		t.Fatal("credentials leaked to another origin", err, other.Header)
	}
}
