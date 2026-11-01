package config

import (
	"runtime"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// TTSProvider 描述一个可选的配音服务。合成本身由编排工具按
// PROMPT-PRODUCTION.md 第三阶段对应小节执行，这里只登记取值与校验所需信息。
//
// Timestamps 是该服务自带时间戳的粒度，决定第四阶段要不要另做强制对齐：
// sentence 只能用于粗排，char 可以直接用作短语级时间轴的依据。
//
// Command 是第三阶段调用的命令名，安全模式下编排工具只被放行这一个 TTS 命令，
// 见 Config.OrchestratorAccess。改提示词里的命令时要同步改这里。
type TTSProvider struct {
	ID         string
	Name       string
	Timestamps string
	Command    string
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
	{ID: "minimax", Name: "MiniMax（mmx CLI）", Timestamps: "sentence", Command: "mmx"},
	{ID: "bailian", Name: "阿里云百炼（bl CLI）", Timestamps: "none", Command: "bl"},
	{ID: "edge", Name: "微软 Edge 在线语音（edge-tts，免鉴权）", Timestamps: "sentence", Command: "edge-tts"},
	{ID: "elevenlabs", Name: "ElevenLabs（官方 CLI）", Timestamps: "char", Command: "elevenlabs"},
	{ID: "tencent", Name: "腾讯云语音合成（tccli）", Timestamps: "sentence", Command: "tccli"},
	{ID: "mlx", Name: "本机 MLX 语音合成（mlx-audio，仅 Apple Silicon）", Timestamps: "none", Command: "mlx_audio.tts.generate"},
}

// Aligner 描述一个已知文本强制对齐工具：文稿是现成的，把它逐字对到配音上
// 比识别更准，也不会把专有名词识别错。
//
// Command 同 TTSProvider.Command。funasr 没有命令行入口，提示词让编排工具用 Python 调用。
type Aligner struct {
	ID      string
	Name    string
	Command string
}

// Aligners 是 ALIGN_PROVIDER 的具体取值；另有 auto 按平台选择。
var Aligners = []Aligner{
	{ID: "mlx", Name: "Qwen3-ForcedAligner（mlx-audio，Apple Silicon 本机）", Command: "mlx_audio.stt.generate"},
	{ID: "funasr", Name: "FunASR 时间戳预测（CPU，macOS 与 Linux）", Command: "python3"},
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

// orchestratorBaseCommands 是任何配置下编排工具都要用到的命令。
var orchestratorBaseCommands = []string{"am", "ffmpeg", "ffprobe", "ls", "mkdir"}

// OrchestratorAccess 返回安全模式下编排工具要放行的命令：基础命令、当前 TTS 与对齐
// 工具各自的那一个命令，加上调用方补充的（固定版本的 hyperframes）。安装命令
// （brew、pip、uv、npm）不在其中：安全模式下要用户自己装好。
func (c Config) OrchestratorAccess(extra ...string) tools.OrchestratorAccess {
	commands := append([]string{}, orchestratorBaseCommands...)
	for _, provider := range TTSProviders {
		if provider.ID == c.TTSProvider && provider.Command != "" {
			commands = append(commands, provider.Command)
		}
	}
	for _, aligner := range Aligners {
		if aligner.ID == c.AlignProvider && aligner.Command != "" {
			commands = append(commands, aligner.Command)
		}
	}
	seen := map[string]bool{}
	out := commands[:0]
	for _, command := range append(commands, extra...) {
		if command != "" && !seen[command] {
			seen[command] = true
			out = append(out, command)
		}
	}
	return tools.OrchestratorAccess{Commands: out}
}
