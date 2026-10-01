package config

import (
	"runtime"
	"strings"
)

// TTSProvider 描述一个可选的配音服务。合成本身由编排工具按
// PROMPT-PRODUCTION.md 第三阶段对应小节执行，这里只登记取值与校验所需信息。
//
// Timestamps 是该服务自带时间戳的粒度，决定第四阶段要不要另做强制对齐：
// sentence 只能用于粗排，char 可以直接用作短语级时间轴的依据。
type TTSProvider struct {
	ID         string
	Name       string
	Timestamps string
}

// TTSProviders 是 TTS_PROVIDER 的全部合法取值，顺序即帮助文字里的顺序。
//
// 收录标准是「有可非交互调用的 CLI，且凭据不必落进项目文件」：
//   - 豆包 / 火山引擎没有官方 CLI，接入要在 am 里写一个无法离线验证的 HTTP 客户端；
//   - Azure 的 spx 把密钥写进当前目录下的 ./spx/data，即项目目录；
//   - IndexTTS2 的许可对商用与模型输出有限制，且只能从源码安装。
//
// 这三家暂不收录。
var TTSProviders = []TTSProvider{
	{ID: "minimax", Name: "MiniMax（mmx CLI）", Timestamps: "sentence"},
	{ID: "bailian", Name: "阿里云百炼（bl CLI）", Timestamps: "none"},
	{ID: "edge", Name: "微软 Edge 在线语音（edge-tts，免鉴权）", Timestamps: "sentence"},
	{ID: "elevenlabs", Name: "ElevenLabs（官方 CLI）", Timestamps: "char"},
	{ID: "tencent", Name: "腾讯云语音合成（tccli）", Timestamps: "sentence"},
	{ID: "mlx", Name: "本机 MLX 语音合成（mlx-audio，仅 Apple Silicon）", Timestamps: "none"},
}

// Aligner 描述一个已知文本强制对齐工具：文稿是现成的，把它逐字对到配音上
// 比识别更准，也不会把专有名词识别错。
type Aligner struct {
	ID   string
	Name string
}

// Aligners 是 ALIGN_PROVIDER 的具体取值；另有 auto 按平台选择。
var Aligners = []Aligner{
	{ID: "mlx", Name: "Qwen3-ForcedAligner（mlx-audio，Apple Silicon 本机）"},
	{ID: "funasr", Name: "FunASR 时间戳预测（CPU，macOS 与 Linux）"},
}

// AlignAuto 让 am 按运行平台选择对齐工具。
const AlignAuto = "auto"

// resolveAligner 把 auto 落成具体工具：Apple Silicon 用 MLX，其余用 FunASR。
func resolveAligner(value, goos, goarch string) string {
	if value != AlignAuto {
		return value
	}
	if goos == "darwin" && goarch == "arm64" {
		return "mlx"
	}
	return "funasr"
}

func validTTSProvider(id string) bool {
	for _, provider := range TTSProviders {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func validAligner(id string) bool {
	for _, aligner := range Aligners {
		if aligner.ID == id {
			return true
		}
	}
	return false
}

// TTSProviderIDs 返回空格分隔的全部取值，用于错误信息与帮助文字。
func TTSProviderIDs() string {
	ids := make([]string, 0, len(TTSProviders))
	for _, provider := range TTSProviders {
		ids = append(ids, provider.ID)
	}
	return strings.Join(ids, " ")
}

// AlignerIDs 返回空格分隔的全部取值（含 auto）。
func AlignerIDs() string {
	ids := []string{AlignAuto}
	for _, aligner := range Aligners {
		ids = append(ids, aligner.ID)
	}
	return strings.Join(ids, " ")
}

// hostAligner 按当前进程的平台解析 auto；测试用 resolveAligner 覆盖其他平台。
func hostAligner(value string) string { return resolveAligner(value, runtime.GOOS, runtime.GOARCH) }
