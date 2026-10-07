package determinism

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func scan(t *testing.T, files map[string]string) Report {
	t.Helper()
	report, err := Scan(writeTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func rules(findings []Finding) []string {
	var ids []string
	for _, f := range findings {
		ids = append(ids, f.File+":"+f.Rule)
	}
	return ids
}

const cleanComposition = `<!doctype html>
<html><head>
<script src="https://cdn.jsdelivr.net/npm/gsap@3.14.2/dist/gsap.min.js"></script>
<style>@font-face { font-family: "Noto"; src: url("fonts/noto.woff2"); }</style>
</head><body>
<div data-composition-id="root" data-duration="3"></div>
<script>
  // 不要用 Math.random()，也不要 Date.now()
  /* requestAnimationFrame 同样禁用 */
  const tl = gsap.timeline({ paused: true });
  tl.to(".a", { x: 100, repeat: 2 });
  const label = "fetch me";
  window.__timelines["root"] = tl;
</script>
</body></html>`

// TestCleanCompositionPasses：固定版本的 CDN、注释里提到的禁用 API、
// 本地字体都不是违规。误判会让人很快不再信任这道门。
func TestCleanCompositionPasses(t *testing.T) {
	report := scan(t, map[string]string{"index.html": cleanComposition, "fonts/noto.woff2": "x"})
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("干净的合成不应有发现：%v", report.Findings)
	}
	if strings.Join(report.Files, ",") != "index.html" {
		t.Fatalf("应只扫描 index.html，实际 %v", report.Files)
	}
}

func TestScriptRulesAreDetectedWithLineNumbers(t *testing.T) {
	cases := []struct {
		name, code, rule string
	}{
		{"clock", "const t = Date.now();", "clock"},
		{"perf", "const t = performance.now();", "clock"},
		{"new-date", "const d = new Date();", "clock"},
		{"random", "const r = Math.random() * 10;", "random"},
		{"raf", "requestAnimationFrame(tick);", "raf"},
		{"interval", "setInterval(step, 16);", "interval"},
		{"repeat", "tl.to('.a', { repeat: -1, yoyo: true });", "infinite-repeat"},
		{"fetch-remote", "fetch('https://example.com/data.json');", "network"},
		{"xhr", "const x = new XMLHttpRequest();", "network"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := scan(t, map[string]string{"index.html": "<html>\n<body>\n<script>\n" + tc.code + "\n</script>\n</body></html>"})
			if report.OK || len(report.Findings) != 1 {
				t.Fatalf("应恰好有一条发现，实际 %v", report.Findings)
			}
			got := report.Findings[0]
			if got.Rule != tc.rule || got.Line != 4 || got.File != "index.html" {
				t.Fatalf("发现不符：%+v", got)
			}
		})
	}
}

// TestLegitimateLookalikesAreNotFlagged：这些写法与禁用 API 形似但无害。
func TestLegitimateLookalikesAreNotFlagged(t *testing.T) {
	for _, code := range []string{
		"const d = new Date(2024, 0, 1);",
		"tl.to('.a', { repeat: -10 === x ? 1 : 2 });",
		"fetch('data/local.json');",
		"setTimeout(() => document.fonts.ready, 0);",
		"cancelAnimationFrame(id);",
		"const rng = mulberry32(42); rng();",
	} {
		report := scan(t, map[string]string{"index.html": "<script>" + code + "</script>"})
		if !report.OK {
			t.Errorf("%q 不应被判违规：%v", code, report.Findings)
		}
	}
}

// TestAllowMarkerMovesFindingToAllowed：viewer 引擎的交互路径合法使用
// requestAnimationFrame，逐行豁免后不判失败，但仍留痕可审计。
func TestAllowMarkerMovesFindingToAllowed(t *testing.T) {
	report := scan(t, map[string]string{"index.html": "<script>\nraf = requestAnimationFrame(tick); // " + AllowMarker + ": 预览播放\n</script>"})
	if !report.OK || len(report.Allowed) != 1 || report.Allowed[0].Rule != "raf" {
		t.Fatalf("豁免行应进入 allowed：%+v", report)
	}
}

// TestOnlyReferencedFilesAreScanned：从入口沿引用展开；没被引用的草稿不参与判定。
func TestOnlyReferencedFilesAreScanned(t *testing.T) {
	report := scan(t, map[string]string{
		"index.html": `<link rel="stylesheet" href="styles/main.css">
<script src="js/anim.js?v=1"></script>
<div data-composition-src="compositions/intro.html"></div>`,
		"js/anim.js":              "import { seed } from './util.mjs';\nconst x = Math.random();",
		"js/util.mjs":             "export const seed = Date.now();",
		"compositions/intro.html": "<script>setInterval(f, 10)</script>",
		"styles/main.css":         "/* url(https://evil.example/a.png) */ body { background: url(https://images.example/bg.png); }",
		"draft.js":                "Math.random(); Date.now();",
	})
	want := []string{"compositions/intro.html:interval", "js/anim.js:random", "js/util.mjs:clock", "styles/main.css:remote-asset"}
	if got := rules(report.Findings); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("发现不符：\n got %v\nwant %v", got, want)
	}
	for _, file := range report.Files {
		if file == "draft.js" {
			t.Fatal("未被引用的 draft.js 不应被扫描")
		}
	}
}

func TestRemoteReferences(t *testing.T) {
	cases := []struct {
		ref, rule string
	}{
		{"https://cdn.jsdelivr.net/npm/gsap@3.14.2/dist/gsap.min.js", ""},
		{"https://unpkg.com/lottie-web@5.12.2/build/player/lottie.min.js", ""},
		{"https://cdnjs.cloudflare.com/ajax/libs/bodymovin/5.12.2/lottie.min.js", ""},
		{"https://cdn.jsdelivr.net/npm/gsap/dist/gsap.min.js", "unpinned-cdn"},
		{"https://cdn.jsdelivr.net/npm/gsap@3/dist/gsap.min.js", "unpinned-cdn"},
		{"https://cdn.jsdelivr.net/npm/gsap@latest/dist/gsap.min.js", "unpinned-cdn"},
		{"https://cdnjs.cloudflare.com/ajax/libs/gsap/latest/gsap.min.js", "unpinned-cdn"},
		{"https://fonts.googleapis.com/css2?family=Inter", "remote-font"},
		{"//images.example.com/a.png", "remote-asset"},
	}
	for _, tc := range cases {
		report := scan(t, map[string]string{"index.html": `<script src="` + tc.ref + `"></script>`})
		got := ""
		if len(report.Findings) > 0 {
			got = report.Findings[0].Rule
		}
		if got != tc.rule {
			t.Errorf("%s：期望 %q，实际 %q", tc.ref, tc.rule, got)
		}
	}
}

func TestImportMapAndStyleAttribute(t *testing.T) {
	report := scan(t, map[string]string{"index.html": `<script type="importmap">{"imports":{"three":"https://cdn.jsdelivr.net/npm/three/build/three.module.js"}}</script>
<script type="application/json">{"note":"Math.random()"}</script>
<div style="background-image:url('https://images.example/x.png')"></div>`})
	want := []string{"index.html:unpinned-cdn", "index.html:remote-asset"}
	if got := rules(report.Findings); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("发现不符：got %v want %v", got, want)
	}
}

// TestHTMLStringsInsideScriptsAreNotAttributes：脚本里拼 HTML 的字符串不是真实引用。
func TestHTMLStringsInsideScriptsAreNotAttributes(t *testing.T) {
	report := scan(t, map[string]string{"index.html": `<script>el.innerHTML = '<img src="https://example.com/a.png">';</script>`})
	if !report.OK {
		t.Fatalf("脚本字符串不应按属性扫描：%v", report.Findings)
	}
}

func TestMissingEntryIsAHintNotAFailure(t *testing.T) {
	report := scan(t, map[string]string{"notes.md": "x"})
	if !report.OK || len(report.Hints) != 1 {
		t.Fatalf("没有入口时应通过并给出提示：%+v", report)
	}
}

// TestNestedEntryIsFound：渲染器先 hyperframes init 出子项目时，入口在下一层。
func TestNestedEntryIsFound(t *testing.T) {
	report := scan(t, map[string]string{
		"project/index.html":             "<script>Date.now()</script>",
		"attempts/attempt-01/index.html": "<script>Math.random()</script>",
	})
	if got := rules(report.Findings); strings.Join(got, ",") != "project/index.html:clock" {
		t.Fatalf("应只扫描子项目入口：%v", got)
	}
}

func TestDuplicateOnSameLineReportedOnce(t *testing.T) {
	report := scan(t, map[string]string{"index.html": "<script>Math.random() + Math.random()</script>"})
	if len(report.Findings) != 1 {
		t.Fatalf("同一行同一规则应只报一次：%v", report.Findings)
	}
}

func TestWriteJSONAndProblems(t *testing.T) {
	report := scan(t, map[string]string{"index.html": "<script>Date.now()</script>"})
	target := filepath.Join(t.TempDir(), "out", "scan.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := report.WriteJSON(target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(body), `"rule": "clock"`) {
		t.Fatalf("报告内容不符：%s %v", body, err)
	}
	problems := report.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "index.html:1 [clock]") {
		t.Fatalf("问题描述不符：%v", problems)
	}
}

// TestEmbeddedViewerTemplatePasses：algorithmic-art 技能要求把 viewer 引擎整份
// 放进镜头目录。它的交互路径逐行写了豁免，扫描必须放行，否则照技能做必挂。
func TestEmbeddedViewerTemplatePasses(t *testing.T) {
	body, err := os.ReadFile("../../assets/shared/.agents/skills/algorithmic-art/templates/viewer.html")
	if err != nil {
		t.Fatal(err)
	}
	report := scan(t, map[string]string{"index.html": string(body)})
	if !report.OK {
		t.Fatalf("viewer 模板不应被判违规：%v", report.Findings)
	}
	if len(report.Allowed) == 0 {
		t.Fatal("viewer 模板的交互路径应以豁免形式留痕")
	}
}

// TestVendoredLibrariesAreSkipped：随镜头拷贝的第三方库内部用到 Math.random、
// requestAnimationFrame 很常见，不在渲染路径上，判定它们只会误判。
func TestVendoredLibrariesAreSkipped(t *testing.T) {
	library := "var a=Date.now||function(){return new Date().getTime()};function r(){return Math.random()*2};requestAnimationFrame(f)"
	report := scan(t, map[string]string{
		"index.html":     `<script src="vendor/gsap.js"></script><script src="gsap.min.js"></script><script src="anime.umd.js"></script><script src="three.js"></script>`,
		"vendor/gsap.js": library,
		"gsap.min.js":    library,
		"anime.umd.js":   library,
		"three.js":       "/*! three.js | MIT license */\n" + library,
	})
	if !report.OK || len(report.Skipped) != 4 {
		t.Fatalf("第三方库应全部跳过并留痕：findings=%v skipped=%v", report.Findings, report.Skipped)
	}
}

// TestStringAndRegexLiteralsDoNotTrigger：审查报告给出的三个误判复现。
func TestStringAndRegexLiteralsDoNotTrigger(t *testing.T) {
	for _, code := range []string{
		"const t='Date.now() is banned';",
		"const caption = `别用 Math.random()`;",
		"var re=/[\"']/g; // Math.random() note",
		"var re=/`/g;\n// don't use Math.random()\nvar s=`x`;",
		"if (/requestAnimationFrame/.test(s)) {}",
	} {
		report := scan(t, map[string]string{"index.html": "<script>" + code + "</script>"})
		if !report.OK {
			t.Errorf("%q 不应被判违规：%v", code, report.Findings)
		}
	}
	// 除号不能被当成正则开头，否则后面的真违规会被吞掉。
	report := scan(t, map[string]string{"index.html": "<script>const half = width / 2; const r = Math.random(); const q = a / b;</script>"})
	if len(report.Findings) != 1 || report.Findings[0].Rule != "random" {
		t.Fatalf("除号之后的真违规必须报出：%v", report.Findings)
	}
}

// TestNonFetchingHrefIsIgnored：<a> 与 canonical 链接渲染时不会被请求。
func TestNonFetchingHrefIsIgnored(t *testing.T) {
	report := scan(t, map[string]string{"index.html": `<a href="https://example.com">x</a>
<link rel="canonical" href="https://example.com/page">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">
<svg><image href="https://images.example/a.png"/></svg>`})
	want := []string{"index.html:remote-font", "index.html:remote-asset"}
	if got := rules(report.Findings); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("只有会被加载的 href 才算引用：got %v want %v", got, want)
	}
}

func TestFontFaceChecks(t *testing.T) {
	report := scan(t, map[string]string{
		"index.html": `<html><head><link rel="stylesheet" href="css/fonts.css"><style>
@font-face { font-family: "Noto Sans SC"; src: url("assets/fonts/noto-400.woff2"); font-display: block; }
@font-face { font-family: "Noto Sans SC"; font-weight: 700; src: url("assets/fonts/noto-700.woff2"); font-display: swap; }
@font-face { font-family: "Inter"; src: local("Inter"), url(assets/fonts/inter.woff2); }
</style></head><body></body></html>`,
		"css/fonts.css":               "@font-face { font-family: Mono; src: url('../assets/fonts/mono.woff2'); font-display: optional; }\n",
		"assets/fonts/noto-400.woff2": "x",
		"assets/fonts/noto-700.woff2": "x",
		"assets/fonts/mono.woff2":     "x",
	})
	got := rules(report.Findings)
	want := []string{"css/fonts.css:font-display", "index.html:font-display", "index.html:font-missing"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("字体检查结果不符：want %v got %v\n%v", want, got, report.Findings)
	}
	for _, f := range report.Findings {
		if f.Rule == "font-missing" && (f.Line != 4 || !strings.Contains(f.Message, "assets/fonts/inter.woff2")) {
			t.Fatalf("缺失字体应点名路径与行号：%+v", f)
		}
	}
}

// TestFontFaceWithoutDisplayPasses：没写 font-display 时 Chrome 按 block 处理，不判。
func TestFontFaceWithoutDisplayPasses(t *testing.T) {
	report := scan(t, map[string]string{
		"index.html": `<style>@font-face { font-family: A; src: url(a.woff2); }</style>`,
		"a.woff2":    "x",
	})
	if !report.OK {
		t.Fatalf("不应判失败：%v", report.Findings)
	}
}
