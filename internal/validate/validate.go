package validate

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/styleimage"
	"gopkg.in/yaml.v3"
)

var secretValue = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{12,}|api[_-]?key\s*[:=]\s*[^\s"']+|password\s*[:=]\s*[^\s"']+)`)

func frontmatter(path string) (map[string]any, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return parseFrontmatter(path, body)
}

func parseFrontmatter(path string, body []byte) (map[string]any, string, error) {
	parts := bytes.SplitN(body, []byte("---"), 3)
	if len(parts) != 3 || len(bytes.TrimSpace(parts[0])) != 0 {
		return nil, "", fmt.Errorf("%s 缺少 YAML frontmatter", path)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(parts[1], &node); err != nil {
		return nil, "", fmt.Errorf("yaml 解析失败：%w", err)
	}
	if err := validateTags(&node); err != nil {
		return nil, "", err
	}
	var result map[string]any
	if err := node.Decode(&result); err != nil {
		return nil, "", err
	}
	return result, string(parts[2]), nil
}

func validateTags(node *yaml.Node) error {
	allowed := map[string]bool{"": true, "!!map": true, "!!seq": true, "!!str": true, "!!int": true, "!!float": true, "!!bool": true, "!!null": true, "!!timestamp": true}
	if !allowed[node.Tag] {
		return fmt.Errorf("不安全的 YAML tag：%s", node.Tag)
	}
	for _, child := range node.Content {
		if err := validateTags(child); err != nil {
			return err
		}
	}
	return nil
}

func safeRelative(root string, value any) error {
	path, ok := value.(string)
	if !ok || path == "" {
		return nil
	}
	if filepath.IsAbs(path) || strings.Contains(path, "://") {
		return fmt.Errorf("路径必须位于项目内：%s", path)
	}
	root, _ = filepath.Abs(root)
	target, _ := filepath.Abs(filepath.Join(root, path))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedTarget := resolveExisting(target)
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("路径逃出项目目录：%s", path)
	}
	return nil
}

// resolveExisting 解析路径里已经存在的那一段，再接上还不存在的部分。
//
// 只解析直接父目录不够：目标在一个还没建出来的子目录里时（例如还没出图的
// covers/），父目录解析失败，路径就停留在未解析的形态；而项目根是解析过的
// （macOS 的临时目录 /var 实际是 /private/var），两者一比就误判成逃出项目。
func resolveExisting(target string) string {
	rest := ""
	for dir := target; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return target
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

func Publish(path, projectRoot string) (map[string]any, error) {
	data, body, err := frontmatter(path)
	if err != nil {
		return nil, err
	}
	required := []string{"schema_version", "workflow", "publish_status", "platform", "generated_at", "video", "cover", "copy", "credits", "manual_checks", "evidence"}
	for _, key := range required {
		if _, ok := data[key]; !ok {
			return nil, fmt.Errorf("缺少必填字段：%s", key)
		}
	}
	if data["schema_version"] != 1 {
		return nil, fmt.Errorf("schema_version 必须为 1")
	}
	statuses := map[string]bool{"draft": true, "pending_manual_checks": true, "blocked": true, "ready": true}
	status, _ := data["publish_status"].(string)
	if !statuses[status] {
		return nil, fmt.Errorf("无效的 publish_status：%v", data["publish_status"])
	}
	rawBody, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if secretValue.MatchString(string(rawBody)) {
		return nil, fmt.Errorf("publish.md 不得包含密钥")
	}
	video, ok := data["video"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("video 必须是对象")
	}
	if err := safeRelative(projectRoot, video["path"]); err != nil {
		return nil, err
	}
	if evidence, ok := data["evidence"].(map[string]any); ok {
		for _, value := range evidence {
			if values, ok := value.([]any); ok {
				for _, item := range values {
					if err := safeRelative(projectRoot, item); err != nil {
						return nil, err
					}
				}
			} else if err := safeRelative(projectRoot, value); err != nil {
				return nil, err
			}
		}
	}
	if err := validateCoverImages(projectRoot, data); err != nil {
		return nil, err
	}
	for _, heading := range []string{"## 发布状态", "## 发布平台", "## 主标题", "## 发布介绍", "## 话题标签", "## 封面文字", "## 素材署名", "## 成片规格与 SHA-256", "## 证据索引", "## 发布前人工检查", "## 未完成事项"} {
		if !strings.Contains(body, heading) {
			return nil, fmt.Errorf("markdown 缺少章节：%s", heading)
		}
	}
	return data, nil
}

func Style(projectRoot string) error {
	frame, _, err := frontmatter(filepath.Join(projectRoot, "frame.md"))
	if err != nil {
		return err
	}
	// 说明书文件名随风格而定，从 frame.md 的 style_id 反查，不写死某一套风格。
	styleID, _ := frame["style_id"].(string)
	style, err := styleOf(styleID)
	if err != nil {
		return fmt.Errorf("frame.md: %w", err)
	}
	guide, _, err := frontmatter(filepath.Join(projectRoot, filepath.FromSlash(style.GuideDoc())))
	if err != nil {
		return err
	}
	active, err := validateStyleSchema(frame)
	if err != nil {
		return fmt.Errorf("frame.md: %w", err)
	}
	if _, err := validateStyleSchema(guide); err != nil {
		return fmt.Errorf("风格说明书: %w", err)
	}
	if !reflect.DeepEqual(frame, guide) {
		return fmt.Errorf("frame.md 与人类可读风格说明的 token 不一致")
	}
	for index, item := range frame["typography"].(map[string]any)["font_files"].([]any) {
		path := item.(map[string]any)["file"].(string)
		if err := safeRelative(projectRoot, path); err != nil {
			return fmt.Errorf("typography.font_files[%d].file: %w", index, err)
		}
		info, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("typography.font_files[%d] 声明的字体文件不存在：%s", index, path)
		}
	}
	if err := validateMoodRefs(projectRoot, frame["mood_refs"]); err != nil {
		return fmt.Errorf("frame.md: %w", err)
	}
	for _, item := range frame["scene_archetypes"].([]any) {
		entry := item.(map[string]any)
		path := entry["example_png"].(string)
		if err := safeRelative(projectRoot, path); err != nil {
			return fmt.Errorf("example_png: %w", err)
		}
		width, height, err := pngDimensions(filepath.Join(projectRoot, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if int(width) != active.Canvas.WidthPx || int(height) != active.Canvas.HeightPx {
			return fmt.Errorf("风格示例必须为 %dx%d，实际 %dx%d：%s",
				active.Canvas.WidthPx, active.Canvas.HeightPx, width, height, path)
		}
	}
	return nil
}

// styleOf 从 frame.md 的 style_id 找出风格身份。
//
// 内置风格查表。自定义风格（custom-*）统一返回 preset.CustomStyle：它的说明书
// 路径固定，不从用户写的 style_name 拼，名字里出现什么字符都不影响找文件。
// 草稿单独拦下并说清还差哪一步——否则用户看到的是示例图缺失之类的连带错误。
func styleOf(id string) (preset.Style, error) {
	if id == preset.DraftStyleID {
		return preset.Style{}, fmt.Errorf("自定义风格尚未定稿（style_id 仍为 %s），定风格阶段未完成："+
			"按 docs/style-brief.md 的描述改写 frame.md 与 %s、画好四张示例图后再校验", id, preset.CustomStyle.GuideDoc())
	}
	if preset.IsCustomStyleID(id) {
		return preset.CustomStyle, nil
	}
	return preset.StyleByIDOrError(id)
}

// moodRefExtensions 是氛围参照图允许的格式：生图工具的产物多为 PNG，
// 也可能是用户自己放进来的照片。
var moodRefExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// validateMoodRefs 校验可选的氛围参照图。
//
// 不查尺寸：生图工具不能指定像素尺寸，参照图也只取质感与装饰语言，不当版式
// 基准用——版式基准是 scene_archetypes 的示例图，那边才严格要求画布尺寸。
func validateMoodRefs(projectRoot string, value any) error {
	if value == nil {
		return nil
	}
	refs, ok := value.([]any)
	if !ok {
		return fmt.Errorf("mood_refs 必须是路径数组")
	}
	for index, item := range refs {
		path, ok := item.(string)
		if !ok || path == "" {
			return fmt.Errorf("mood_refs[%d] 必须是非空字符串", index)
		}
		if err := safeRelative(projectRoot, path); err != nil {
			return fmt.Errorf("mood_refs[%d]: %w", index, err)
		}
		if !moodRefExtensions[strings.ToLower(filepath.Ext(path))] {
			return fmt.Errorf("mood_refs[%d] 必须是 PNG / JPEG / WebP 图片：%s", index, path)
		}
		info, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("mood_refs[%d] 声明的参照图不存在：%s", index, path)
		}
	}
	return nil
}

// validateStyleSchema 校验风格 token，并返回该 frame.md 对应的画幅预设，
// 供调用方继续按画幅校验示例图尺寸。
func validateStyleSchema(tokens map[string]any) (preset.Preset, error) {
	required := []string{"schema_version", "style_id", "style_name", "scope", "canvas", "safe_area", "colors", "typography", "spacing", "radius", "scene_archetypes", "motion", "audio", "subtitles", "cover", "forbidden"}
	if err := requireKeys(tokens, required, ""); err != nil {
		return preset.Preset{}, err
	}
	if tokens["schema_version"] != 1 {
		return preset.Preset{}, fmt.Errorf("schema_version 必须为 1")
	}
	if _, ok := tokens["style_id"].(string); !ok {
		return preset.Preset{}, fmt.Errorf("style_id 必须是字符串")
	}
	if _, ok := tokens["style_name"].(string); !ok {
		return preset.Preset{}, fmt.Errorf("style_name 必须是字符串")
	}
	if scope, ok := tokens["scope"].([]any); !ok || len(scope) == 0 {
		return preset.Preset{}, fmt.Errorf("scope 必须是非空数组")
	}
	canvas, err := requireMap(tokens["canvas"], "canvas")
	if err != nil {
		return preset.Preset{}, err
	}
	width, widthOK := canvas["width_px"].(int)
	height, heightOK := canvas["height_px"].(int)
	fps, fpsOK := canvas["fps"].(int)
	orientation, orientationOK := canvas["orientation"].(string)
	if !widthOK || !heightOK || !fpsOK || !orientationOK {
		return preset.Preset{}, fmt.Errorf("canvas 必须包含整数 width_px、height_px、fps 与字符串 orientation")
	}
	active, ok := preset.ByCanvas(width, height, fps, orientation)
	if !ok {
		return preset.Preset{}, fmt.Errorf("canvas %dx%d、%dfps、%s 不是内置画幅，可选：%s",
			width, height, fps, orientation, strings.Join(preset.IDs(), " "))
	}
	safeArea, err := requireMap(tokens["safe_area"], "safe_area")
	if err != nil {
		return preset.Preset{}, err
	}
	// 逐值比对 anchor 推导结果，而不只是检查字段齐全非负：手改一个 top_px
	// 在旧校验下完全查不出来，成片才会发现字幕压线。
	expected, err := active.ResolveSafeArea()
	if err != nil {
		return preset.Preset{}, err
	}
	if len(safeArea) != len(expected) {
		return preset.Preset{}, fmt.Errorf("safe_area 必须恰好包含 %d 个安全区，实际 %d 个", len(expected), len(safeArea))
	}
	for _, item := range expected {
		box, err := requireMap(safeArea[item.Name], "safe_area."+item.Name)
		if err != nil {
			return preset.Preset{}, err
		}
		if err := requireKeys(box, []string{"left_px", "right_px", "top_px", "bottom_px"}, "safe_area."+item.Name); err != nil {
			return preset.Preset{}, err
		}
		for _, field := range []struct {
			name string
			want int
		}{
			{"left_px", item.Box.LeftPx}, {"right_px", item.Box.RightPx},
			{"top_px", item.Box.TopPx}, {"bottom_px", item.Box.BottomPx},
		} {
			got, ok := box[field.name].(int)
			if !ok {
				return preset.Preset{}, fmt.Errorf("safe_area.%s.%s 必须是整数", item.Name, field.name)
			}
			if got != field.want {
				return preset.Preset{}, fmt.Errorf("safe_area.%s.%s = %d，按 %s 画幅应为 %d",
					item.Name, field.name, got, active.ID, field.want)
			}
		}
	}
	colors, err := requireMap(tokens["colors"], "colors")
	if err != nil || len(colors) == 0 {
		return preset.Preset{}, fmt.Errorf("colors 必须是非空对象")
	}
	for name, raw := range colors {
		value, ok := raw.(string)
		if !ok || !hexColor.MatchString(value) {
			return preset.Preset{}, fmt.Errorf("colors.%s 必须是六位十六进制颜色", name)
		}
	}
	typography, err := requireMap(tokens["typography"], "typography")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := requireKeys(typography, []string{"primary_stack", "mono_stack", "sizes_px", "weights", "line_heights", "font_files"}, "typography"); err != nil {
		return preset.Preset{}, err
	}
	if err := validateFontStacks(typography); err != nil {
		return preset.Preset{}, err
	}
	for _, metric := range []struct {
		name string
		min  float64
	}{{"sizes_px", 1}, {"weights", 1}, {"line_heights", .5}} {
		mapping, err := requireMap(typography[metric.name], "typography."+metric.name)
		if err != nil {
			return preset.Preset{}, err
		}
		if err := validateNumericMap(mapping, "typography."+metric.name, metric.min); err != nil {
			return preset.Preset{}, err
		}
	}
	for _, name := range []string{"spacing", "radius"} {
		mapping, err := requireMap(tokens[name], name)
		if err != nil {
			return preset.Preset{}, err
		}
		if err := validateNumericMap(mapping, name, 0); err != nil {
			return preset.Preset{}, err
		}
	}
	radius := tokens["radius"].(map[string]any)
	if radius["pill_px"] != 999 {
		return preset.Preset{}, fmt.Errorf("radius.pill_px 必须为 999")
	}
	archetypes, ok := tokens["scene_archetypes"].([]any)
	canonical := preset.ArchetypeIDs
	if !ok || len(archetypes) != len(canonical) {
		return preset.Preset{}, fmt.Errorf("scene_archetypes 必须包含四种标准类型")
	}
	for index, item := range archetypes {
		entry, err := requireMap(item, fmt.Sprintf("scene_archetypes[%d]", index))
		if err != nil {
			return preset.Preset{}, err
		}
		if entry["id"] != canonical[index] {
			return preset.Preset{}, fmt.Errorf("scene_archetypes 必须按标准顺序声明")
		}
		if _, ok := entry["name"].(string); !ok {
			return preset.Preset{}, fmt.Errorf("scene_archetypes[%d].name 必须是字符串", index)
		}
		if _, ok := entry["example_png"].(string); !ok {
			return preset.Preset{}, fmt.Errorf("scene_archetypes[%d].example_png 必须是字符串", index)
		}
	}
	motion, err := requireMap(tokens["motion"], "motion")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := requireKeys(motion, []string{"phases", "entrance_seconds", "exit_seconds", "transition_seconds", "entrance_ease", "exit_ease", "verbs"}, "motion"); err != nil {
		return preset.Preset{}, err
	}
	phases, err := requireMap(motion["phases"], "motion.phases")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := validateNumericMap(phases, "motion.phases", 0); err != nil {
		return preset.Preset{}, err
	}
	total := 0.0
	for _, value := range phases {
		total += number(value)
	}
	if total < .999999 || total > 1.000001 {
		return preset.Preset{}, fmt.Errorf("motion.phases 之和必须为 1")
	}
	audio, err := requireMap(tokens["audio"], "audio")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := requireKeys(audio, []string{"sample_rate_hz", "channels", "final_integrated_lufs", "final_lufs_tolerance", "true_peak_max_dbtp", "bgm_ducking_db", "bgm_fade_seconds", "sfx_max_simultaneous"}, "audio"); err != nil {
		return preset.Preset{}, err
	}
	if audio["sample_rate_hz"] != 48000 || audio["channels"] != 2 {
		return preset.Preset{}, fmt.Errorf("audio 必须为 48000Hz 双声道")
	}
	subtitles, err := requireMap(tokens["subtitles"], "subtitles")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := requireKeys(subtitles, []string{"required", "font_size_px", "max_lines", "max_fullwidth_chars_per_line", "line_height", "background", "text_color", "padding_px", "radius_px", "break_rules"}, "subtitles"); err != nil {
		return preset.Preset{}, err
	}
	cover, err := requireMap(tokens["cover"], "cover")
	if err != nil {
		return preset.Preset{}, err
	}
	if err := requireKeys(cover, []string{"frame_zero_complete", "default_lines", "max_lines", "font_size_px", "max_width_px", "contrast_ratio_min", "stable_frames"}, "cover"); err != nil {
		return preset.Preset{}, err
	}
	stableFrames, stable := cover["stable_frames"].(int)
	if cover["frame_zero_complete"] != true || !stable || stableFrames < 18 {
		return preset.Preset{}, fmt.Errorf("cover 必须从第零帧完整显示并稳定至少 18 帧")
	}
	forbidden, ok := tokens["forbidden"].([]any)
	if !ok || len(forbidden) == 0 {
		return preset.Preset{}, fmt.Errorf("forbidden 必须是非空字符串数组")
	}
	for _, item := range forbidden {
		if _, ok := item.(string); !ok {
			return preset.Preset{}, fmt.Errorf("forbidden 必须是非空字符串数组")
		}
	}
	return active, nil
}

// autoEmbeddedFonts 是 HyperFrames 0.8.14 会自动下载并内联的字体族
// （CANONICAL_FONTS，背后是 @fontsource/* 包）。清单里没有任何简体中文字体：
// 唯一的 CJK 项 Noto Sans JP 缺失大量简体字，不能当替代品。
var autoEmbeddedFonts = map[string]bool{
	"archivo black": true, "eb garamond": true, "ibm plex mono": true, "inter": true,
	"jetbrains mono": true, "lato": true, "league gothic": true, "montserrat": true,
	"noto sans jp": true, "nunito": true, "open sans": true, "oswald": true,
	"outfit": true, "playfair display": true, "poppins": true, "roboto": true,
	"source code pro": true, "space mono": true,
}

// genericFontFamilies 是 CSS 内建的兜底关键字，不需要字体文件。
var genericFontFamilies = map[string]bool{
	"sans-serif": true, "serif": true, "monospace": true, "cursive": true,
	"fantasy": true, "system-ui": true, "ui-sans-serif": true, "ui-serif": true,
	"ui-monospace": true, "emoji": true, "math": true, "fangsong": true,
}

func fontStackFamilies(stack string) []string {
	parts := strings.Split(stack, ",")
	families := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.ToLower(strings.Trim(strings.TrimSpace(part), `"'`))
		if name != "" {
			families = append(families, name)
		}
	}
	return families
}

// validateFontStacks 保证字体栈里的每个字体族要么被 HyperFrames 自动内联，
// 要么由项目自带文件。渲染机是干净的无头 Chrome：写一个只存在于本机的系统字体
// （PingFang SC、Hiragino Sans GB 等），本地渲染会因为回退而"看着正常"，
// 云端和 CI 上排版却是错的，而且没有任何报错。
func validateFontStacks(typography map[string]any) error {
	declared := map[string]bool{}
	entries, ok := typography["font_files"].([]any)
	if !ok || len(entries) == 0 {
		return fmt.Errorf("typography.font_files 必须是非空数组")
	}
	for index, item := range entries {
		entry, err := requireMap(item, fmt.Sprintf("typography.font_files[%d]", index))
		if err != nil {
			return err
		}
		family, familyOK := entry["family"].(string)
		file, fileOK := entry["file"].(string)
		weight := number(entry["weight"])
		if !familyOK || family == "" || !fileOK || file == "" || weight < 1 {
			return fmt.Errorf("typography.font_files[%d] 必须声明 family、weight 和 file", index)
		}
		declared[strings.ToLower(family)] = true
	}
	used := map[string]bool{}
	// display_stack 可选：游戏与编辑类风格的展示字（像素字、手写字、粗黑展示体）
	// 不能放进 primary_stack 的首位，否则渲染器会拿它排正文。
	keys := []string{"primary_stack", "mono_stack"}
	if _, ok := typography["display_stack"]; ok {
		keys = append(keys, "display_stack")
	}
	for _, key := range keys {
		stack, ok := typography[key].(string)
		if !ok || stack == "" {
			return fmt.Errorf("typography.%s 必须是非空字符串", key)
		}
		for _, family := range fontStackFamilies(stack) {
			if genericFontFamilies[family] {
				continue
			}
			used[family] = true
			if autoEmbeddedFonts[family] || declared[family] {
				continue
			}
			return fmt.Errorf("typography.%s 使用了既不会被 HyperFrames 自动内联、"+
				"也没有在 typography.font_files 中自带文件的字体：%s（渲染机没有本机系统字体，"+
				"排版会静默回退）", key, family)
		}
	}
	for family := range declared {
		if !used[family] {
			return fmt.Errorf("typography.font_files 声明了字体 %s，但没有任何字体栈使用它", family)
		}
	}
	return nil
}

func requireMap(value any, name string) (map[string]any, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是对象", name)
	}
	return mapping, nil
}

func requireKeys(mapping map[string]any, keys []string, name string) error {
	for _, key := range keys {
		if _, ok := mapping[key]; !ok {
			if name == "" {
				return fmt.Errorf("缺少必填字段：%s", key)
			}
			return fmt.Errorf("缺少必填字段：%s.%s", name, key)
		}
	}
	return nil
}

func number(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case float64:
		return typed
	default:
		return -1
	}
}

func validateNumericMap(mapping map[string]any, name string, minimum float64) error {
	for key, value := range mapping {
		numeric := number(value)
		if math.IsNaN(numeric) || math.IsInf(numeric, 0) || numeric < minimum {
			return fmt.Errorf("%s.%s 必须是大于等于 %g 的数字", name, key, minimum)
		}
	}
	return nil
}

func pngDimensions(path string) (uint32, uint32, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	header := make([]byte, 24)
	if _, err := io.ReadFull(file, header); err != nil {
		return 0, 0, fmt.Errorf("读取 PNG %s: %w", path, err)
	}
	if !bytes.Equal(header[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return 0, 0, fmt.Errorf("不是 PNG：%s", path)
	}
	return binary.BigEndian.Uint32(header[16:20]), binary.BigEndian.Uint32(header[20:24]), nil
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// RegenerateExamples recreates the style SVGs and renders their PNG
// counterparts. The embedded SVGs are immutable templates, so regeneration
// never depends on files left by an earlier run.
//
// 项目改过配色时，示例图要跟着换色：以内置 frame.md 的配色为基准，把 SVG 里
// 每个基准色替换成项目 frame.md 同名 token 的值。基准取自项目所用风格自己的
// 内置 frame.md，而不是写死某一套风格的色板。
//
// 自定义风格没有内置模板，示例 SVG 是定风格阶段画进项目的：直接渲染项目里的
// SVG，不改写、不重着色。草稿也放行，画示例图本来就在定稿之前。
func RegenerateExamples(projectRoot string, output io.Writer) error {
	frame, _, err := frontmatter(filepath.Join(projectRoot, "frame.md"))
	if err != nil {
		return err
	}
	colors, ok := frame["colors"].(map[string]any)
	if !ok {
		return fmt.Errorf("frame.md 的 colors 必须是对象")
	}
	active, err := frameCanvas(frame)
	if err != nil {
		return err
	}
	styleID, _ := frame["style_id"].(string)
	if preset.IsCustomStyleID(styleID) {
		return regenerateCustomExamples(projectRoot, active, colors, output)
	}
	style, err := preset.StyleByIDOrError(styleID)
	if err != nil {
		return fmt.Errorf("frame.md: %w", err)
	}
	templateDir := "assets/styles/" + style.ID + "/" + active.ID
	recolor, err := recolorMap(templateDir+"/frame.md", colors)
	if err != nil {
		return err
	}
	source := func(name string) (string, error) {
		body, readErr := assets.Files.ReadFile(templateDir + "/assets/style-guide/examples/" + name + ".svg")
		if readErr != nil {
			return "", fmt.Errorf("读取内置示例 %s: %w", name, readErr)
		}
		return recolor.Replace(string(body)), nil
	}
	return renderExamples(projectRoot, active, source, recolor.Replace(style.SheetBackground), output)
}

// frameCanvas 从项目 frame.md 的 canvas 反查画幅预设。
//
// 示例图模板按画幅分目录，必须从项目自己的 canvas 反查，不能取默认预设：
// 拿 3:4 的模板铺进 9:16 项目，Style 会立刻拒绝，但用户看到的是尺寸报错而
// 不是「模板取错了」。
func frameCanvas(frame map[string]any) (preset.Preset, error) {
	canvas, ok := frame["canvas"].(map[string]any)
	if !ok {
		return preset.Preset{}, fmt.Errorf("frame.md 的 canvas 必须是对象")
	}
	width, _ := canvas["width_px"].(int)
	height, _ := canvas["height_px"].(int)
	fps, _ := canvas["fps"].(int)
	orientation, _ := canvas["orientation"].(string)
	active, ok := preset.ByCanvas(width, height, fps, orientation)
	if !ok {
		return preset.Preset{}, fmt.Errorf("frame.md 的画幅 %dx%d 不是内置画幅，无法定位示例模板", width, height)
	}
	return active, nil
}

// regenerateCustomExamples 渲染自定义风格画进项目的示例 SVG。
// 缺的 SVG 一次列全，免得「补一张、重跑、再发现下一张」。
func regenerateCustomExamples(projectRoot string, active preset.Preset, colors map[string]any, output io.Writer) error {
	dir := filepath.Join(projectRoot, "assets", "style-guide", "examples")
	var missing []string
	for _, name := range preset.ArchetypeIDs {
		if info, err := os.Stat(filepath.Join(dir, name+".svg")); err != nil || !info.Mode().IsRegular() {
			missing = append(missing, "assets/style-guide/examples/"+name+".svg")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("自定义风格的示例图要先画成 SVG，缺少：%s", strings.Join(missing, "、"))
	}
	source := func(name string) (string, error) {
		body, err := os.ReadFile(filepath.Join(dir, name+".svg"))
		return string(body), err
	}
	background := preset.CustomStyle.SheetBackground
	if value, ok := colors["canvas"].(string); ok && hexColor.MatchString(value) {
		background = value
	}
	return renderExamples(projectRoot, active, source, background, output)
}

// renderExamples 把四种骨架的 SVG 写进项目并渲染成 PNG，再拼 contact sheet。
// source 返回的内容与项目里已有的 SVG 相同时不重写，项目自带的 SVG 保持原样。
func renderExamples(projectRoot string, active preset.Preset, source func(string) (string, error), background string, output io.Writer) error {
	outputDir := filepath.Join(projectRoot, "assets", "style-guide", "examples")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	if err := styleimage.CheckRenderer(); err != nil {
		return err
	}
	projectFonts, _ := filepath.Glob(filepath.Join(projectRoot, "assets", "fonts", "*.woff2"))
	fontDir, cleanup, err := styleimage.DecompressFonts(projectFonts)
	if err != nil {
		return err
	}
	defer cleanup()
	pngPaths := make([]string, 0, len(preset.ArchetypeIDs))
	for _, name := range preset.ArchetypeIDs {
		svg, err := source(name)
		if err != nil {
			return err
		}
		svgPath := filepath.Join(outputDir, name+".svg")
		pngPath := filepath.Join(outputDir, name+".png")
		if existing, readErr := os.ReadFile(svgPath); readErr != nil || string(existing) != svg {
			if err := os.WriteFile(svgPath, []byte(svg), 0o644); err != nil {
				return err
			}
		}
		if err := styleimage.RenderSVG(svgPath, pngPath, fontDir); err != nil {
			return err
		}
		fmt.Fprintf(output, "generated %s\ngenerated %s\n", filepath.ToSlash(filepath.Join("assets", "style-guide", "examples", name+".svg")), filepath.ToSlash(filepath.Join("assets", "style-guide", "examples", name+".png")))
		pngPaths = append(pngPaths, pngPath)
	}
	contactPath := filepath.Join(outputDir, "contact-sheet.png")
	const thumbnailWidthPx = 405
	thumb := fmt.Sprintf("%dx%d", thumbnailWidthPx, thumbnailWidthPx*active.Canvas.HeightPx/active.Canvas.WidthPx)
	if err := styleimage.ContactSheet(pngPaths, contactPath, thumb, background, "4x1"); err != nil {
		return err
	}
	fmt.Fprintf(output, "generated %s\n", filepath.ToSlash(filepath.Join("assets", "style-guide", "examples", "contact-sheet.png")))
	return nil
}

// recolorMap 以内置 frame.md 的配色为基准，生成「基准色 → 项目色」的替换器。
// 基准色按大小写不敏感匹配，SVG 里手写的十六进制色大小写不一。
func recolorMap(embeddedFrame string, colors map[string]any) (*strings.Replacer, error) {
	body, err := assets.Files.ReadFile(embeddedFrame)
	if err != nil {
		return nil, fmt.Errorf("读取内置风格 %s: %w", embeddedFrame, err)
	}
	builtin, _, err := parseFrontmatter(embeddedFrame, body)
	if err != nil {
		return nil, err
	}
	baseline, ok := builtin["colors"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("内置风格 %s 的 colors 不是对象", embeddedFrame)
	}
	names := make([]string, 0, len(baseline))
	for name := range baseline {
		names = append(names, name)
	}
	sort.Strings(names)
	var pairs []string
	for _, name := range names {
		original, _ := baseline[name].(string)
		value, ok := colors[name].(string)
		if !ok || !hexColor.MatchString(value) {
			return nil, fmt.Errorf("frame.md 的 colors.%s 必须是六位十六进制颜色", name)
		}
		if strings.EqualFold(original, value) {
			continue
		}
		pairs = append(pairs, strings.ToUpper(original), value, strings.ToLower(original), value)
	}
	return strings.NewReplacer(pairs...), nil
}
