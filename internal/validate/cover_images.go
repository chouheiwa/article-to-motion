package validate

import (
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码，DecodeConfig 才认得 .jpg 封面
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// minCoverShortSide 是平台封面短边的下限。低于 1080 的封面在手机全屏和
// 网页端都会被放大发虚；B 站常见的 1146×717 因此不够，16:10 用 1920×1200。
const minCoverShortSide = 1080

// coverSpec 是一个平台接受的封面比例，以及 ready 前必须备齐的比例。
type coverSpec struct {
	name     string
	allowed  []string
	required []string
}

// coverSpecs 是平台封面规格表。依据是 2026-10 对各平台上传界面与创作者
// 资料的检索（官方帮助页多需登录，按两个以上来源一致取值）：
//
//	抖音：网页端上传要横封面（有效区 4:3）与竖封面（3:4，个人主页与双列信息流）
//	小红书：首页只透出 3:4、1:1、4:3，其他比例会被裁切；3:4 信息流占比最大
//	视频号：竖版 3:4，横版 16:9；只有一个封面选择区域
//	B 站：上传 16:10，各展示位再裁成 16:9 或 4:3，标题要放在中间安全区
//	知乎：视频封面 16:10（来源单一，可信度低）
//
// 平台规则变了改这里，制作提示词第十二阶段的表格同步改。
var coverSpecs = map[string]coverSpec{
	"douyin":      {name: "抖音", allowed: []string{"4:3", "3:4"}, required: []string{"4:3", "3:4"}},
	"xiaohongshu": {name: "小红书", allowed: []string{"3:4", "1:1", "4:3"}, required: []string{"3:4"}},
	"channels":    {name: "微信视频号", allowed: []string{"3:4", "16:9"}, required: []string{"3:4"}},
	"bilibili":    {name: "B 站", allowed: []string{"16:10"}, required: []string{"16:10"}},
	"zhihu":       {name: "知乎", allowed: []string{"16:10"}, required: []string{"16:10"}},
}

var coverExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true}

func coverPlatforms() string {
	names := make([]string, 0, len(coverSpecs))
	for k := range coverSpecs {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, " / ")
}

// validateCoverImages 校验 publish.md 里可选的 cover.images：平台封面图。
//
// 尺寸以文件实测为准，声明值只用来对账：手填的宽高与实际不符，说明封面
// 改过却没回填 publish.md，发布时上传的就不是记录里那张。同一张图可以
// 登记给多个平台（例如 3:4 竖封面同时给抖音与小红书）。
func validateCoverImages(projectRoot string, data map[string]any) error {
	cover, _ := data["cover"].(map[string]any)
	have := map[string]map[string]bool{}
	if raw, present := cover["images"]; present && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("cover.images 必须是数组")
		}
		for index, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("cover.images[%d] 必须是对象", index)
			}
			platform, ratio, err := checkCoverImage(projectRoot, index, entry)
			if err != nil {
				return err
			}
			if have[platform] == nil {
				have[platform] = map[string]bool{}
			}
			have[platform][ratio] = true
		}
	}
	if data["publish_status"] != "ready" {
		return nil
	}
	platform, _ := data["platform"].(string)
	spec, known := coverSpecs[platform]
	if !known {
		return nil
	}
	var missing []string
	for _, ratio := range spec.required {
		if !have[platform][ratio] {
			missing = append(missing, ratio)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s发布还缺 %s 封面，cover.images 不全时不能是 ready", spec.name, strings.Join(missing, "、"))
	}
	return nil
}

func checkCoverImage(projectRoot string, index int, entry map[string]any) (string, string, error) {
	platform, _ := entry["platform"].(string)
	ratio, _ := entry["ratio"].(string)
	path, _ := entry["path"].(string)
	if platform == "" || ratio == "" || path == "" {
		return "", "", fmt.Errorf("cover.images[%d] 必须声明 platform、ratio 与 path", index)
	}
	spec, known := coverSpecs[platform]
	if !known {
		return "", "", fmt.Errorf("cover.images[%d] 的平台 %q 不在规格表里，可选：%s", index, platform, coverPlatforms())
	}
	if !contains(spec.allowed, ratio) {
		return "", "", fmt.Errorf("cover.images[%d]：%s封面只接受 %s，不接受 %s", index, spec.name, strings.Join(spec.allowed, "、"), ratio)
	}
	rw, rh, err := parseRatio(ratio)
	if err != nil {
		return "", "", fmt.Errorf("cover.images[%d]: %w", index, err)
	}
	if orientation, ok := entry["orientation"].(string); ok && orientation != orientationOf(rw, rh) {
		return "", "", fmt.Errorf("cover.images[%d] 的 orientation %s 与比例 %s 矛盾", index, orientation, ratio)
	}
	if err := safeRelative(projectRoot, path); err != nil {
		return "", "", fmt.Errorf("cover.images[%d]: %w", index, err)
	}
	if !coverExtensions[strings.ToLower(filepath.Ext(path))] {
		return "", "", fmt.Errorf("cover.images[%d] 必须是 PNG / JPEG 图片：%s", index, path)
	}
	file, err := os.Open(filepath.Join(projectRoot, filepath.FromSlash(path)))
	if err != nil {
		return "", "", fmt.Errorf("cover.images[%d] 声明的封面不存在：%s", index, path)
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return "", "", fmt.Errorf("cover.images[%d] 读不出图片尺寸：%s：%w", index, path, err)
	}
	w, h := config.Width, config.Height
	if dw, dh := intField(entry["width_px"]), intField(entry["height_px"]); dw != w || dh != h {
		return "", "", fmt.Errorf("cover.images[%d] 声明 %dx%d，实际 %dx%d：%s", index, dw, dh, w, h, path)
	}
	if w*rh != h*rw {
		return "", "", fmt.Errorf("cover.images[%d] 必须正好 %s，实际 %dx%d：%s", index, ratio, w, h, path)
	}
	if min(w, h) < minCoverShortSide {
		return "", "", fmt.Errorf("cover.images[%d] 短边不足 %d 像素（实际 %dx%d）：%s", index, minCoverShortSide, w, h, path)
	}
	return platform, ratio, nil
}

func parseRatio(ratio string) (int, int, error) {
	parts := strings.Split(ratio, ":")
	if len(parts) == 2 {
		w, errW := strconv.Atoi(parts[0])
		h, errH := strconv.Atoi(parts[1])
		if errW == nil && errH == nil && w > 0 && h > 0 {
			return w, h, nil
		}
	}
	return 0, 0, fmt.Errorf("ratio 必须写成「宽:高」，例如 4:3：%q", ratio)
}

func orientationOf(w, h int) string {
	switch {
	case w > h:
		return "landscape"
	case w < h:
		return "portrait"
	}
	return "square"
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func intField(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return -1
}
