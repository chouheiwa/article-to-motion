package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/determinism"
)

// TestSceneKitPassesDeterminismScan 把 scene-kit 放进一个照技能说明搭起来的
// 镜头目录，跑一遍 am 自己的确定性源码扫描。
//
// 技能要求渲染器把 scene-kit.js 复制进每个镜头目录，所以它会被每个镜头的
// 扫描读到。库里一旦混进时钟、未播种随机数或定时器，所有用它的镜头都会被
// 判失败——必须在这里先炸，而不是在用户的渲染里。example.html 是 SKILL.md
// 指定的写法参考，照着抄的镜头也必须能过同一道门。
func TestSceneKitPassesDeterminismScan(t *testing.T) {
	shared, err := assets.Shared()
	if err != nil {
		t.Fatal(err)
	}
	skill := skillTreeRoot + "/scene-kit/"
	sceneDir := t.TempDir()
	copies := map[string]string{
		skill + "scene-kit.js": "scene-kit.js",
		skill + "example.html": "index.html",
	}
	for from, to := range copies {
		body, err := fs.ReadFile(shared, from)
		if err != nil {
			t.Fatalf("读 %s：%v", from, err)
		}
		if err := os.WriteFile(filepath.Join(sceneDir, to), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 示例引用的字体按技能说明随镜头目录提供；缺了它，扫描会如实报字体文件缺失。
	fonts := filepath.Join(sceneDir, "assets", "fonts")
	if err := os.MkdirAll(fonts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range exampleFontFiles(t, shared, skill+"example.html") {
		if err := os.WriteFile(filepath.Join(fonts, name), []byte("woff2"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// am-scene.js 由 am 在渲染前生成，这里只放一个占位，让引用能解析到本地文件。
	if err := os.WriteFile(filepath.Join(sceneDir, "am-scene.js"), []byte("window.AM_SCENE = {schema: 1};\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := determinism.Scan(sceneDir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("scene-kit 没通过确定性扫描：\n%v", report.Problems())
	}
	scanned := false
	for _, file := range report.Files {
		if file == "scene-kit.js" {
			scanned = true
		}
	}
	if !scanned {
		t.Fatalf("扫描没有读到 scene-kit.js（可能被当成第三方库跳过）：files=%v skipped=%v", report.Files, report.Skipped)
	}
	if len(report.Allowed) != 0 {
		t.Errorf("scene-kit 不应依赖逐行豁免：%v", report.Allowed)
	}
}

// exampleFontFiles 读出示例 @font-face 引用的 assets/fonts/ 下的文件名。
func exampleFontFiles(t *testing.T, shared fs.FS, path string) []string {
	t.Helper()
	body, err := fs.ReadFile(shared, path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`assets/fonts/([\w.-]+\.woff2)`).FindAllStringSubmatch(string(body), -1) {
		names = append(names, m[1])
	}
	return names
}
