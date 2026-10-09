package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishWithCopy 在模板基础上写入平台与 copy 块里的标题行。
func publishWithCopy(t *testing.T, platform, copyLines string) string {
	t.Helper()
	root := t.TempDir()
	body, err := os.ReadFile(sharedSource("templates", "publish.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := replaceOnce(string(body), "platform: unspecified", "platform: "+platform)
	text = replaceOnce(text, "  primary_title: \"\"\n", copyLines)
	path := filepath.Join(root, "publish.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPublishAcceptsTitlesWithinPlatformLimits(t *testing.T) {
	// 抖音 30 字整：上限本身要放行
	douyin30 := strings.Repeat("考", 25) + "Vibe!"
	path := publishWithCopy(t, "douyin", "  primary_title: \""+douyin30+"\"\n  platform_titles:\n    xiaohongshu: \"考什么，就造什么：考核为什么总被造假\"\n    bilibili: \""+strings.Repeat("长", 60)+"\"\n")
	if _, err := Publish(path, filepath.Dir(path)); err != nil {
		t.Fatalf("上限以内的标题被拒：%v", err)
	}
}

func TestPublishRejectsOverlongTitles(t *testing.T) {
	path := publishWithCopy(t, "douyin", "  primary_title: \"Vibe知识大赏｜考什么，就造什么：为什么考核什么，就会有人造假什么\"\n  platform_titles:\n    xiaohongshu: \"Vibe知识大赏｜考什么，就造什么：考核为什么\"\n")
	_, err := Publish(path, filepath.Dir(path))
	if err == nil {
		t.Fatal("超长标题应当被拒")
	}
	// 两条都要报，且报出实际字数
	for _, want := range []string{"抖音标题最多 30 个字，当前 34 个字", "小红书标题最多 20 个字，当前 23 个字"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误里缺少 %q：%v", want, err)
		}
	}
}

func TestPublishRejectsUnknownTitlePlatform(t *testing.T) {
	path := publishWithCopy(t, "douyin", "  primary_title: \"短标题\"\n  platform_titles:\n    tiktok: \"short\"\n")
	if _, err := Publish(path, filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), "未知平台") {
		t.Fatalf("未知平台应当被拒，得到：%v", err)
	}
}
