package align

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Item 是对齐工具输出的一条带时间的文本（通常是一个字或一个词），时间单位为秒，
// 已加上所在段的偏移。
type Item struct {
	Text    string
	Start   float64
	End     float64
	Segment int
}

// Segment 是一个对齐结果文件及其在整条配音里的起点。
type Segment struct {
	Path   string
	Offset float64
}

// ParseSegmentFlag 解析 --align 的取值 FILE[@OFFSET_SECONDS]。
func ParseSegmentFlag(value string) (Segment, error) {
	at := strings.LastIndex(value, "@")
	if at < 0 {
		return Segment{Path: value}, nil
	}
	path, raw := value[:at], value[at+1:]
	offset, err := strconv.ParseFloat(raw, 64)
	if err != nil || path == "" || offset < 0 || math.IsInf(offset, 0) || math.IsNaN(offset) {
		return Segment{}, fmt.Errorf("--align %q 不合法：应为 文件[@偏移秒数]，偏移是非负数", value)
	}
	return Segment{Path: path, Offset: offset}, nil
}

// CanonicalShape 写进帮助与报错：认不出的对齐结果统一转成这个形状。
const CanonicalShape = `[{"text":"字","start":0.12,"end":0.20}, ...]（秒）`

// LoadSegments 按顺序读入各段对齐结果，平移后首尾相接。
func LoadSegments(segments []Segment) ([]Item, error) {
	var items []Item
	for index, seg := range segments {
		loaded, err := LoadEvidence(seg.Path)
		if err != nil {
			return nil, err
		}
		for _, item := range loaded {
			item.Start += seg.Offset
			item.End += seg.Offset
			item.Segment = index
			items = append(items, item)
		}
	}
	return items, nil
}

// LoadEvidence 读取一个对齐结果文件，返回未平移的条目。
//
// 各对齐工具的输出格式没有逐一实测过，所以读取是宽容的：
//   - 规范形状 [{"text","start","end"}]，秒；
//   - funasr：[{"text":"空格 分隔 的 字","timestamp":[[起毫秒,止毫秒],...]}]，可嵌在任意层；
//   - 其他：在 JSON 里找第一个「每个元素都有文本字段（text/word/char/token）和数值起止」
//     的对象数组。起止字段名为 start/end、start_time/end_time 时按秒，start_ms/end_ms 按毫秒。
func LoadEvidence(path string) ([]Item, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取对齐结果：%w", err)
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%s 不是合法 JSON：%w", path, err)
	}
	items, ok, err := findItems(root)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", path, err)
	}
	if !ok {
		return nil, fmt.Errorf("%s 的结构认不出（%s）。请先转换成 %s", path, describe(root), CanonicalShape)
	}
	for i, item := range items {
		if !finite(item.Start) || !finite(item.End) || item.Start < 0 || item.End < item.Start {
			return nil, fmt.Errorf("%s 第 %d 条（%q）的起止时间不合法：%v → %v", path, i+1, item.Text, item.Start, item.End)
		}
		// 六小时以上的秒数几乎一定是把毫秒当成了秒。
		if item.End > 6*3600 {
			return nil, fmt.Errorf("%s 第 %d 条的结束时间 %v 秒超过 6 小时，像是毫秒；请转换成 %s", path, i+1, item.End, CanonicalShape)
		}
	}
	return items, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// findItems 深度优先找第一个认得的数组。对象的键按字典序遍历，结果与 JSON 键顺序无关。
func findItems(node any) ([]Item, bool, error) {
	switch value := node.(type) {
	case []any:
		if items, ok, err := funasrItems(value); ok || err != nil {
			return items, ok, err
		}
		if items, ok := timedItems(value); ok {
			return items, true, nil
		}
		for _, child := range value {
			if items, ok, err := findItems(child); ok || err != nil {
				return items, ok, err
			}
		}
	case map[string]any:
		for _, key := range sortedKeys(value) {
			if items, ok, err := findItems(value[key]); ok || err != nil {
				return items, ok, err
			}
		}
	}
	return nil, false, nil
}

var textKeys = []string{"text", "word", "char", "token"}

type timeKeys struct {
	start, end string
	scale      float64
}

var timeKeyPairs = []timeKeys{
	{"start", "end", 1},
	{"start_time", "end_time", 1},
	{"start_ms", "end_ms", 0.001},
}

// timedItems 认「每个元素都有文本与数值起止」的对象数组。
func timedItems(values []any) ([]Item, bool) {
	if len(values) == 0 {
		return nil, false
	}
	items := make([]Item, 0, len(values))
	for _, raw := range values {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		text, ok := textField(object)
		if !ok {
			return nil, false
		}
		matched := false
		for _, keys := range timeKeyPairs {
			start, okStart := object[keys.start].(float64)
			end, okEnd := object[keys.end].(float64)
			if okStart && okEnd {
				items = append(items, Item{Text: text, Start: start * keys.scale, End: end * keys.scale})
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	return items, true
}

func textField(object map[string]any) (string, bool) {
	for _, key := range textKeys {
		if text, ok := object[key].(string); ok {
			return text, true
		}
	}
	return "", false
}

// funasrItems 认 funasr 时间戳预测的输出：text 以空格分隔，timestamp 是等长的毫秒区间表。
func funasrItems(values []any) ([]Item, bool, error) {
	if len(values) == 0 {
		return nil, false, nil
	}
	var items []Item
	for _, raw := range values {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		text, okText := object["text"].(string)
		stamps, okStamps := object["timestamp"].([]any)
		if !okText || !okStamps {
			return nil, false, nil
		}
		words := strings.Fields(text)
		if len(words) != len(stamps) {
			return nil, false, fmt.Errorf("funasr 结果的 text 有 %d 个单元，timestamp 却有 %d 组，无法逐一对应", len(words), len(stamps))
		}
		for i, stamp := range stamps {
			pair, ok := stamp.([]any)
			if !ok || len(pair) != 2 {
				return nil, false, fmt.Errorf("funasr 结果第 %d 个 timestamp 不是 [起, 止] 两个数", i+1)
			}
			start, okStart := pair[0].(float64)
			end, okEnd := pair[1].(float64)
			if !okStart || !okEnd {
				return nil, false, fmt.Errorf("funasr 结果第 %d 个 timestamp 不是数值", i+1)
			}
			items = append(items, Item{Text: words[i], Start: start / 1000, End: end / 1000})
		}
	}
	return items, true, nil
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// describe 给出顶层结构的简述，报错时让操作者知道该怎么转换。
func describe(node any) string {
	switch value := node.(type) {
	case map[string]any:
		return "顶层是对象，键：" + strings.Join(sortedKeys(value), ", ")
	case []any:
		if len(value) == 0 {
			return "顶层是空数组"
		}
		if first, ok := value[0].(map[string]any); ok {
			return fmt.Sprintf("顶层是 %d 个元素的数组，首元素的键：%s", len(value), strings.Join(sortedKeys(first), ", "))
		}
		return fmt.Sprintf("顶层是 %d 个元素的数组，首元素类型 %T", len(value), value[0])
	default:
		return fmt.Sprintf("顶层是 %T", node)
	}
}
