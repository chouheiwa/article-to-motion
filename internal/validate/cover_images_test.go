package validate

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeImage 写一张指定尺寸的空白 PNG。
func writeImage(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
}

type coverImage struct {
	platform, ratio, path string
	w, h                  int
	extra                 string // 追加到条目里的字段，例如 orientation
}

// publishWithCovers 在模板基础上声明封面图，并按声明尺寸写出图片（create 为真时）。
func publishWithCovers(t *testing.T, status, platform string, images []coverImage, create bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	body, err := os.ReadFile(sharedSource("templates", "publish.md"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("  images:\n")
	for _, img := range images {
		fmt.Fprintf(&b, "    - {platform: %s, ratio: \"%s\", path: %s, width_px: %d, height_px: %d%s}\n", img.platform, img.ratio, img.path, img.w, img.h, img.extra)
		if create {
			writeImage(t, filepath.Join(root, filepath.FromSlash(img.path)), img.w, img.h)
		}
	}
	text := replaceOnce(string(body), "  images: []\n", b.String())
	text = replaceOnce(text, "publish_status: draft", "publish_status: "+status)
	text = replaceOnce(text, "platform: unspecified", "platform: "+platform)
	path := filepath.Join(root, "publish.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestPublishAcceptsEachPlatformsCovers(t *testing.T) {
	cases := map[string][]coverImage{
		"douyin":      {{"douyin", "4:3", "covers/douyin-1440x1080.png", 1440, 1080, ""}, {"douyin", "3:4", "covers/douyin-1080x1440.png", 1080, 1440, ""}},
		"xiaohongshu": {{"xiaohongshu", "3:4", "covers/xhs-1080x1440.png", 1080, 1440, ""}},
		"channels":    {{"channels", "3:4", "covers/channels-1080x1440.png", 1080, 1440, ""}},
		"bilibili":    {{"bilibili", "16:10", "covers/bilibili-1920x1200.png", 1920, 1200, ""}},
		"zhihu":       {{"zhihu", "16:10", "covers/zhihu-1920x1200.png", 1920, 1200, ""}},
	}
	for platform, images := range cases {
		t.Run(platform, func(t *testing.T) {
			root, path := publishWithCovers(t, "ready", platform, images, true)
			if _, err := Publish(path, root); err != nil {
				t.Fatalf("%s 封面齐全应通过：%v", platform, err)
			}
		})
	}
}

// 同一张 3:4 竖封面可以登记给多个平台。
func TestPublishAllowsOneImageForSeveralPlatforms(t *testing.T) {
	images := []coverImage{
		{"douyin", "4:3", "covers/a-4x3.png", 1440, 1080, ""},
		{"douyin", "3:4", "covers/a-3x4.png", 1080, 1440, ""},
		{"xiaohongshu", "3:4", "covers/a-3x4.png", 1080, 1440, ""},
	}
	root, path := publishWithCovers(t, "ready", "douyin", images, true)
	if _, err := Publish(path, root); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRejectsBadCoverImages(t *testing.T) {
	cases := []struct {
		name     string
		img      coverImage
		actualW  int
		actualH  int
		create   bool
		wantPart string
	}{
		{"平台不支持该比例", coverImage{"bilibili", "4:3", "covers/a.png", 1440, 1080, ""}, 1440, 1080, true, "16:10"},
		{"未知平台", coverImage{"weibo", "16:9", "covers/a.png", 1920, 1080, ""}, 1920, 1080, true, "douyin"},
		{"比例与尺寸不符", coverImage{"douyin", "4:3", "covers/a.png", 1440, 1000, ""}, 1440, 1000, true, "4:3"},
		{"朝向与比例矛盾", coverImage{"douyin", "4:3", "covers/a.png", 1440, 1080, ", orientation: portrait"}, 1440, 1080, true, "orientation"},
		{"声明与实际不符", coverImage{"douyin", "4:3", "covers/a.png", 1440, 1080, ""}, 1600, 1200, true, "实际"},
		{"分辨率太低", coverImage{"bilibili", "16:10", "covers/a.png", 1152, 720, ""}, 1152, 720, true, "1080"},
		{"文件不存在", coverImage{"douyin", "4:3", "covers/a.png", 1440, 1080, ""}, 0, 0, false, "不存在"},
		{"逃出项目", coverImage{"douyin", "4:3", "../a.png", 1440, 1080, ""}, 0, 0, false, "项目"},
		{"不是图片", coverImage{"douyin", "4:3", "covers/a.txt", 1440, 1080, ""}, 0, 0, false, "PNG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, path := publishWithCovers(t, "draft", "douyin", []coverImage{tc.img}, false)
			if tc.create {
				writeImage(t, filepath.Join(root, filepath.FromSlash(tc.img.path)), tc.actualW, tc.actualH)
			}
			_, err := Publish(path, root)
			if err == nil || !strings.Contains(err.Error(), tc.wantPart) {
				t.Fatalf("应报含 %q 的错误，实际：%v", tc.wantPart, err)
			}
		})
	}
}

// 状态声称 ready 却缺发布平台要的封面，要到上传时才会发现。
func TestPublishReadyRequiresPlatformsCovers(t *testing.T) {
	cases := []struct {
		platform string
		images   []coverImage
		want     string
	}{
		{"douyin", []coverImage{{"douyin", "4:3", "covers/a.png", 1440, 1080, ""}}, "3:4"},
		{"xiaohongshu", []coverImage{{"xiaohongshu", "1:1", "covers/a.png", 1080, 1080, ""}}, "3:4"},
		{"bilibili", nil, "16:10"},
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			root, path := publishWithCovers(t, "ready", tc.platform, tc.images, true)
			if _, err := Publish(path, root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ready 却缺 %s 封面应报错，实际：%v", tc.want, err)
			}
			// 草稿阶段放行。
			root2, path2 := publishWithCovers(t, "draft", tc.platform, tc.images, true)
			if _, err := Publish(path2, root2); err != nil {
				t.Fatalf("草稿阶段缺封面应放行：%v", err)
			}
		})
	}
}
