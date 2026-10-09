package validate

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// titleLimit 是一个平台发布标题的字数上限，按字符计（一个汉字、一个字母、
// 一个标点都算一个字）。平台输入框就是这么数的，按字节或按「汉字算一、
// 字母算半」数都会放过超长标题，上传时被截断或拒收。
type titleLimit struct {
	name string
	max  int
}

// titleLimits 是平台标题字数表，依据 2026-10 作者在各平台上传时给出的上限：
//
//	抖音：标题最多 30 字
//	小红书：标题最多 20 字
//
// 没有列出的平台不查字数。平台规则变了改这里，制作提示词「发布配置」一节同步改。
var titleLimits = map[string]titleLimit{
	"douyin":      {name: "抖音", max: 30},
	"xiaohongshu": {name: "小红书", max: 20},
}

func titlePlatforms() string {
	names := make([]string, 0, len(titleLimits))
	for k := range titleLimits {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, " / ")
}

// validateCopyTitles 按平台字数上限校验发布标题。
//
// copy.primary_title 对照 publish.md 的 platform；可选的 copy.platform_titles
// 为同一条视频发多个平台时逐平台写标题，每条对照它自己的平台。一次报出全部
// 超长项，免得改一条、重跑一次、再发现下一条。
func validateCopyTitles(data map[string]any) error {
	copyBlock, ok := data["copy"].(map[string]any)
	if !ok {
		return fmt.Errorf("copy 必须是对象")
	}
	var problems []string
	check := func(field, platform string, value any) {
		limit, known := titleLimits[platform]
		if !known {
			return
		}
		title, _ := value.(string)
		if n := utf8.RuneCountInString(strings.TrimSpace(title)); n > limit.max {
			problems = append(problems, fmt.Sprintf("%s：%s标题最多 %d 个字，当前 %d 个字（%s）", field, limit.name, limit.max, n, title))
		}
	}
	platform, _ := data["platform"].(string)
	check("copy.primary_title", platform, copyBlock["primary_title"])
	if raw, present := copyBlock["platform_titles"]; present && raw != nil {
		titles, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("copy.platform_titles 必须是「平台: 标题」的对象")
		}
		keys := make([]string, 0, len(titles))
		for k := range titles {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, known := titleLimits[k]; !known {
				if _, cover := coverSpecs[k]; !cover {
					problems = append(problems, fmt.Sprintf("copy.platform_titles 里有未知平台 %q（已知：%s）", k, titlePlatforms()))
					continue
				}
			}
			title, ok := titles[k].(string)
			if !ok {
				problems = append(problems, fmt.Sprintf("copy.platform_titles.%s 必须是字符串", k))
				continue
			}
			check("copy.platform_titles."+k, k, title)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("发布标题超出平台字数：\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}
