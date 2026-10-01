package determinism

import (
	"net/url"
	"regexp"
	"strings"
)

// AllowMarker 写在某一行的注释里，豁免这一行的全部发现。
//
// 豁免存在的理由：algorithmic-art 的 viewer 引擎在交互路径上合法地用了
// requestAnimationFrame 和 Math.random（播放预览、「随机种子」按钮），渲染路径
// 走的是 __art.renderFrame，不经过它们。没有豁免的话，照技能说明把 viewer 放进
// 镜头目录就必然被判失败——误判一次，这道门就没人信了。
//
// 豁免必须逐行写、写在代码旁边，并且会出现在报告的 allowed 里，可审计。
const AllowMarker = "am-allow-nondeterministic"

// Rule 是一条源码级确定性规则。NeedsStrings 的规则要看字符串字面量
// （网络规则看 fetch 的 URL），在只剥了注释的文本上匹配；其余规则在连字符串
// 内容也抹掉的文本上匹配，画面文案里提到禁用 API 不算违规。
type Rule struct {
	ID           string
	Pattern      *regexp.Regexp
	Message      string
	NeedsStrings bool
}

// scriptRules 作用于脚本正文（内联 <script> 与本地 JS 文件，注释已剥除）。
//
// 每条都对应 hyperframes-core 的 determinism-rules.md 里一条禁令，且都是
// HyperFrames 自己的 lint 不查的：lint 查 repeat 取整溢出，不查无限 repeat；
// 时钟、随机数、定时器与运行时网络请求它一条都不看。
//
// 刻意没有收录 setTimeout：等字体、等资源就绪的代码里常见它，并不决定画面状态，
// 收录的话误判远多于真问题。setInterval 则几乎只用于驱动画面变化。
var scriptRules = []Rule{
	{"clock", regexp.MustCompile(`\bDate\.now\s*\(|\bperformance\.now\s*\(|\bnew\s+Date\s*\(\s*\)`),
		"读取了真实时钟；渲染按帧 seek，画面状态只能由时间轴位置决定", false},
	{"random", regexp.MustCompile(`\bMath\.random\s*\(`),
		"使用了未播种的 Math.random()；随机效果请用固定种子的 PRNG", false},
	{"raf", regexp.MustCompile(`\brequestAnimationFrame\s*\(`),
		"使用了 requestAnimationFrame；动画必须挂在 paused timeline 上由渲染器 seek", false},
	{"interval", regexp.MustCompile(`\bsetInterval\s*\(`),
		"使用了 setInterval；定时器按真实时间触发，与渲染帧无关", false},
	{"infinite-repeat", regexp.MustCompile(`\brepeat\s*:\s*-\s*1\b`),
		"repeat: -1 是无限循环；请按镜头时长算出有限次数", false},
	{"network", regexp.MustCompile(`\bfetch\s*\(\s*['"` + "`" + `](?:https?:)?//|\bnew\s+(?:XMLHttpRequest|WebSocket|EventSource)\b`),
		"渲染时发起网络请求；所需资源必须随镜头目录提供", true},
}

// pinnedCDNs 是允许远程引用的脚本 CDN，且必须带完整版本号。
//
// 上游技能的最小合成示例就是从 jsdelivr 引固定版本的 GSAP，把远程脚本一律
// 判死等于判死几乎所有镜头。版本号是底线：不带版本或写 @latest 时，同一镜头
// 隔一周重渲染会拿到另一份库，那才是不可复现。
var (
	semverInPath = regexp.MustCompile(`@\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?(?:/|$)`)
	cdnjsVersion = regexp.MustCompile(`^/ajax/libs/[^/]+/\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?/`)
)

// remoteProblem 判定一个远程 URL 是否违规，返回规则 ID 与说明；合规时返回空串。
func remoteProblem(raw string) (string, string) {
	target := raw
	if strings.HasPrefix(target, "//") {
		target = "https:" + target
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return "remote-asset", "无法解析的远程地址；所需资源必须随镜头目录提供"
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "fonts.googleapis.com", "fonts.gstatic.com":
		return "remote-font", "引用了在线字体；字体必须用 @font-face 指向镜头目录内的文件"
	case "cdn.jsdelivr.net", "unpkg.com", "esm.sh":
		if semverInPath.MatchString(parsed.Path) {
			return "", ""
		}
		return "unpinned-cdn", "CDN 引用没有固定到完整版本号；库内容会随上游漂移，请写成 包名@x.y.z 的形式"
	case "cdnjs.cloudflare.com":
		if cdnjsVersion.MatchString(parsed.Path) {
			return "", ""
		}
		return "unpinned-cdn", "CDN 引用没有固定到完整版本号；库内容会随上游漂移，请写成 包名@x.y.z 的形式"
	}
	return "remote-asset", "引用了远程资源；所需资源必须随镜头目录提供，或内联进合成"
}

// isRemote 报告引用是否指向网络。
func isRemote(ref string) bool {
	lower := strings.ToLower(ref)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//")
}

// isIgnoredRef 报告引用是否与文件无关：页内锚点、内联数据与伪协议。
func isIgnoredRef(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if lower == "" || strings.HasPrefix(lower, "#") {
		return true
	}
	for _, prefix := range []string{"data:", "blob:", "javascript:", "mailto:", "about:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// 模板插值（${...}、{{...}}）在静态扫描时无法求值，不猜。
	return strings.Contains(ref, "${") || strings.Contains(ref, "{{")
}
