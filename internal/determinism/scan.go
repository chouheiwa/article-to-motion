// Package determinism 静态扫描镜头合成源码，拦下会让渲染结果不可复现的写法。
//
// HyperFrames 按帧 seek 出图：第 N 帧的画面只能由时间轴位置决定。读时钟、
// 未播种的随机数、requestAnimationFrame、定时器和渲染时网络请求都会让同一帧在
// 两次渲染间不同。这些禁令写在上游 hyperframes-core 技能里，但只是提示词——
// 渲染器照样能写出来，渲染照样退出码 0，问题要到重渲染对不上时才暴露。
//
// 扫描范围只取渲染真正加载的文件：从合成入口出发，沿 src / href /
// data-composition-src / import / url() 的本地引用逐个展开。镜头目录里没被
// 引用的草稿、技能模板副本不参与判定，避免误伤。
package determinism

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// Finding 是一条违规，File 为相对镜头目录的斜杠路径。
type Finding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Snippet string `json:"snippet"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d [%s] %s：%s", f.File, f.Line, f.Rule, f.Message, f.Snippet)
}

// Report 是一次扫描的机器可读结果。OK 只由 Findings 决定。
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	OK            bool      `json:"ok"`
	Entries       []string  `json:"entries"`
	Files         []string  `json:"files"`
	Findings      []Finding `json:"findings"`
	Allowed       []Finding `json:"allowed,omitempty"`
	// Skipped 是按第三方库跳过规则判定的文件。
	Skipped []string `json:"skipped,omitempty"`
	Hints   []string `json:"hints,omitempty"`
}

// ReportSchemaVersion 是 Report 的结构版本。
const ReportSchemaVersion = 1

// Problems 把违规逐条格式化成给人看的文字。
func (r Report) Problems() []string {
	problems := make([]string, 0, len(r.Findings))
	for _, finding := range r.Findings {
		problems = append(problems, "源码不确定："+finding.String())
	}
	return problems
}

// WriteJSON 原子写出报告。
func (r Report) WriteJSON(target string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(target, append(body, '\n'), 0o644)
}

// skippedDirs 是寻找入口时不进入的目录：重试归档、抽帧产物、渲染输出与依赖。
var skippedDirs = map[string]bool{
	"attempts": true, "visual-qc": true, "node_modules": true, "renders": true, "snapshots": true,
}

// entryDepth 是寻找 index.html 的最大目录深度。渲染器有时先 hyperframes init
// 出一个子项目再在里面写合成，入口就落在镜头目录下一层。
const entryDepth = 2

// Scan 扫描 dir 下的合成源码。找不到入口不算失败，只写进 Hints：
// 这道门的前提是「宁可漏判，不可误判」。
func Scan(dir string) (Report, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return Report{}, err
	}
	report := Report{SchemaVersion: ReportSchemaVersion, Entries: []string{}, Files: []string{}, Findings: []Finding{}}
	entries, err := findEntries(root)
	if err != nil {
		return Report{}, err
	}
	if len(entries) == 0 {
		report.Hints = append(report.Hints, "镜头目录里找不到 index.html 或顶层 .html 合成入口，未做源码扫描")
		report.OK = true
		return report, nil
	}
	s := &scanner{root: root, seen: map[string]bool{}, reported: map[string]bool{}, report: &report}
	for _, entry := range entries {
		report.Entries = append(report.Entries, entry)
		s.queue = append(s.queue, entry)
	}
	for len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		if s.seen[next] {
			continue
		}
		s.seen[next] = true
		if err := s.scanFile(next); err != nil {
			return Report{}, err
		}
	}
	sort.Strings(report.Files)
	sortFindings(report.Findings)
	sortFindings(report.Allowed)
	report.OK = len(report.Findings) == 0
	return report, nil
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
}

// findEntries 优先取镜头目录的 index.html；没有时取顶层全部 .html；
// 仍没有时向下找 entryDepth 层内的 index.html。
func findEntries(root string) ([]string, error) {
	if isRegular(filepath.Join(root, "index.html")) {
		return []string{"index.html"}, nil
	}
	items, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("无法读取镜头目录：%w", err)
	}
	var entries []string
	for _, item := range items {
		if !item.IsDir() && strings.EqualFold(filepath.Ext(item.Name()), ".html") {
			entries = append(entries, item.Name())
		}
	}
	if len(entries) > 0 {
		return entries, nil
	}
	err = filepath.WalkDir(root, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, current)
		depth := len(strings.Split(filepath.ToSlash(rel), "/"))
		if d.IsDir() {
			if current != root && (skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || depth > entryDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "index.html" && depth <= entryDepth+1 {
			entries = append(entries, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(entries)
	return entries, err
}

func isRegular(target string) bool {
	info, err := os.Stat(target)
	return err == nil && info.Mode().IsRegular()
}

type scanner struct {
	root     string
	seen     map[string]bool
	reported map[string]bool
	queue    []string
	report   *Report
}

// source 是一个待扫描文件的原文与逐行文本，用于行号换算与豁免判定。
type source struct {
	rel   string
	raw   string
	lines []string
}

func (s source) lineOf(offset int) int {
	return strings.Count(s.raw[:min(offset, len(s.raw))], "\n") + 1
}

func (s *scanner) scanFile(rel string) error {
	body, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("读取合成源码失败：%w", err)
	}
	src := source{rel: rel, raw: string(body), lines: strings.Split(string(body), "\n")}
	s.report.Files = append(s.report.Files, rel)
	switch strings.ToLower(path.Ext(rel)) {
	case ".html", ".htm":
		s.scanHTML(src)
	case ".js", ".mjs", ".cjs":
		if vendored(rel, src.raw) {
			// 第三方库内部的 Math.random、requestAnimationFrame 不在渲染路径上，
			// 照常判定只会制造误判；只记下跳过了它，留痕可查。
			s.report.Skipped = append(s.report.Skipped, rel)
			return nil
		}
		s.scanScript(src, src.raw, 0)
	case ".css":
		s.scanCSS(src, stripCSSComments(src.raw), 0)
	}
	return nil
}

var (
	scriptBlock  = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	attrType     = regexp.MustCompile(`(?i)\btype\s*=\s*["']?([^"'\s>]+)`)
	attrSrc      = regexp.MustCompile(`(?i)(?:^|\s)src\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	htmlTag      = regexp.MustCompile(`(?s)<([A-Za-z][\w:-]*)([^>]*)>`)
	resourceAttr = regexp.MustCompile(`(?i)(?:^|[\s"'])(src|href|xlink:href|poster|data-composition-src)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	linkRel      = regexp.MustCompile(`(?i)\brel\s*=\s*["']?([^"'>]*)`)
	cssURL       = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"\s]+))\s*\)|@import\s+(?:"([^"]*)"|'([^']*)')`)
	jsImport     = regexp.MustCompile(`\bimport\s*(?:[\w*{}\s,$]+\s*from\s*)?(?:\(\s*)?["']([^"']+)["']`)
	jsonURL      = regexp.MustCompile(`"((?:https?:)?//[^"]+|\.{0,2}/[^"]+)"`)
)

// nonScriptTypes 是内容不是可执行 JS 的 <script type>。
var nonScriptTypes = map[string]bool{
	"application/json": true, "application/ld+json": true, "text/template": true,
	"text/html": true, "text/x-template": true, "text/plain": true,
}

func (s *scanner) scanHTML(src source) {
	text := stripHTMLComments(src.raw)
	markup := []byte(text)
	for _, loc := range scriptBlock.FindAllStringSubmatchIndex(text, -1) {
		attrs := text[loc[2]:loc[3]]
		bodyStart, bodyEnd := loc[4], loc[5]
		// 脚本正文里常有拼 HTML 的字符串，不能让属性扫描看到它们。
		blank(markup, bodyStart, bodyEnd)
		if match := attrSrc.FindStringSubmatch(attrs); match != nil {
			s.reference(src, loc[2], firstNonEmpty(match[1:]...))
			continue
		}
		kind := ""
		if match := attrType.FindStringSubmatch(attrs); match != nil {
			kind = strings.ToLower(match[1])
		}
		body := text[bodyStart:bodyEnd]
		switch {
		case kind == "importmap":
			for _, m := range jsonURL.FindAllStringSubmatchIndex(body, -1) {
				s.reference(src, bodyStart+m[2], body[m[2]:m[3]])
			}
		case nonScriptTypes[kind]:
		default:
			s.scanScript(src, body, bodyStart)
		}
	}
	rest := string(markup)
	for _, tag := range htmlTag.FindAllStringSubmatchIndex(rest, -1) {
		name := strings.ToLower(rest[tag[2]:tag[3]])
		attrs := rest[tag[4]:tag[5]]
		for _, m := range resourceAttr.FindAllStringSubmatchIndex(attrs, -1) {
			attr := strings.ToLower(attrs[m[2]:m[3]])
			if strings.HasSuffix(attr, "href") && !fetchesHref(name, attrs) {
				continue
			}
			value, start := submatch(attrs, m[2:])
			s.reference(src, tag[4]+start, value)
		}
	}
	s.scanCSS(src, stripCSSComments(rest), 0)
}

func (s *scanner) scanScript(src source, script string, base int) {
	withStrings, codeOnly := lexJS(script)
	for _, rule := range scriptRules {
		text := codeOnly
		if rule.NeedsStrings {
			text = withStrings
		}
		for _, loc := range rule.Pattern.FindAllStringIndex(text, -1) {
			s.add(src, base+loc[0], rule.ID, rule.Message)
		}
	}
	for _, m := range jsImport.FindAllStringSubmatchIndex(withStrings, -1) {
		s.reference(src, base+m[2], withStrings[m[2]:m[3]])
	}
}

func (s *scanner) scanCSS(src source, css string, base int) {
	for _, m := range cssURL.FindAllStringSubmatchIndex(css, -1) {
		value, start := submatch(css, m)
		s.reference(src, base+start, value)
	}
}

// submatch 返回第一个非空捕获组及其起点。
func submatch(text string, m []int) (string, int) {
	for i := 2; i+1 < len(m); i += 2 {
		if m[i] >= 0 && m[i+1] > m[i] {
			return text[m[i]:m[i+1]], m[i]
		}
	}
	return "", m[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// reference 处理一个资源引用：远程的按 CDN 规则判定，本地的入队继续展开。
func (s *scanner) reference(src source, offset int, ref string) {
	ref = strings.TrimSpace(ref)
	if isIgnoredRef(ref) {
		return
	}
	if isRemote(ref) {
		if rule, message := remoteProblem(ref); rule != "" {
			s.add(src, offset, rule, message+"（"+ref+"）")
		}
		return
	}
	target := ref
	if cut := strings.IndexAny(target, "?#"); cut >= 0 {
		target = target[:cut]
	}
	if target == "" || strings.Contains(target, ":") {
		return
	}
	var resolved string
	if strings.HasPrefix(target, "/") {
		resolved = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		resolved = path.Clean(path.Join(path.Dir(src.rel), target))
	}
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return
	}
	switch strings.ToLower(path.Ext(resolved)) {
	case ".html", ".htm", ".js", ".mjs", ".cjs", ".css":
	default:
		return
	}
	if isRegular(filepath.Join(s.root, filepath.FromSlash(resolved))) && !s.seen[resolved] {
		s.queue = append(s.queue, resolved)
	}
}

func (s *scanner) add(src source, offset int, rule, message string) {
	line := src.lineOf(offset)
	text := ""
	if line-1 < len(src.lines) {
		text = src.lines[line-1]
	}
	snippet := strings.TrimSpace(text)
	if runes := []rune(snippet); len(runes) > 120 {
		snippet = string(runes[:120]) + "…"
	}
	finding := Finding{File: src.rel, Line: line, Rule: rule, Message: message, Snippet: snippet}
	// 同一行同一规则只报一次：一行里连写两次 Math.random() 是同一个问题。
	key := fmt.Sprintf("%s:%d:%s:%s", src.rel, line, rule, message)
	if s.reported[key] {
		return
	}
	s.reported[key] = true
	if strings.Contains(text, AllowMarker) {
		s.report.Allowed = append(s.report.Allowed, finding)
		return
	}
	s.report.Findings = append(s.report.Findings, finding)
}

// fetchesHref 报告这个标签的 href 会不会在渲染时被加载。<a> 和
// <link rel="canonical"> 这类只是元数据，渲染器不会去请求，算作引用就是误判。
func fetchesHref(tag, attrs string) bool {
	switch tag {
	case "image", "use", "feimage":
		return true
	case "link":
		match := linkRel.FindStringSubmatch(attrs)
		if match == nil {
			return false
		}
		for _, rel := range strings.Fields(strings.ToLower(match[1])) {
			switch rel {
			case "stylesheet", "preload", "modulepreload", "icon", "import":
				return true
			}
		}
	}
	return false
}

// vendorDirs 下的文件、以及带 .min / .bundle / .umd 后缀或许可证横幅的文件，
// 视为随镜头拷贝的第三方库。
var (
	vendorDirs   = map[string]bool{"vendor": true, "vendors": true, "lib": true, "libs": true, "node_modules": true, "third_party": true}
	vendorSuffix = regexp.MustCompile(`(?i)[.-](min|bundle|umd|prod|production)\.(m|c)?js$`)
)

// vendored 报告 rel 是否是第三方库。自己写的合成代码不会落在这些位置、
// 也不会带许可证横幅；误把第三方库当成作者代码判定，代价远高于漏检。
func vendored(rel, body string) bool {
	for _, part := range strings.Split(path.Dir(rel), "/") {
		if vendorDirs[strings.ToLower(part)] {
			return true
		}
	}
	if vendorSuffix.MatchString(rel) {
		return true
	}
	head := strings.TrimSpace(body[:min(len(body), 512)])
	return strings.HasPrefix(head, "/*!") || strings.HasPrefix(head, "/**!") ||
		(strings.HasPrefix(head, "/*") && strings.Contains(strings.ToLower(head), "@license"))
}
