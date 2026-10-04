package beat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
)

// AnalyzerVersion 是固定的 beat-this 版本。版本不符时脚本直接失败，
// 不同版本的模型给出的网格不同，静默换版会让已确认的网格失去复现依据。
const AnalyzerVersion = "1.1.0"

// analysisSampleRate 是 beat-this 内部使用的采样率；按它解码可省掉一次重采样。
const analysisSampleRate = 22050

// analyzeScript 读单声道 16 位 WAV，把普通拍与重拍写进 argv[3] 指向的 JSON。
//
// 结果写文件而不是打印到 stdout：首次运行会下载模型权重，下载进度与库的日志
// 会混进标准输出，按 stdout 解析时结果就丢了。argv[2] 为 "--doctor" 时只加载
// 模型，用来提前下载权重。
const analyzeScript = `import importlib.metadata as m, json, sys, wave
v = m.version('beat-this')
if v != '` + AnalyzerVersion + `':
    sys.exit('beat-this ' + v + ' != ` + AnalyzerVersion + `')
import numpy as np
from beat_this.inference import Audio2Beats
if sys.argv[1] == '--doctor':
    Audio2Beats(checkpoint_path=sys.argv[2], device='cpu', dbn=False)
    sys.exit(0)
with wave.open(sys.argv[1], 'rb') as w:
    if w.getnchannels() != 1 or w.getsampwidth() != 2:
        sys.exit('analysis wav must be mono 16-bit')
    sr = w.getframerate()
    signal = np.frombuffer(w.readframes(w.getnframes()), dtype='<i2').astype(np.float32) / 32768
beats, downbeats = Audio2Beats(checkpoint_path=sys.argv[2], device='cpu', dbn=False)(signal, sr)
with open(sys.argv[3], 'w') as f:
    json.dump({'beats': [float(b) for b in beats], 'downbeats': [float(d) for d in downbeats]}, f)
`

// Media 是分析与试听用到的媒体操作；mediaprobe.Toolchain 满足它。
type Media interface {
	DecodeAnalysisWAV(ctx context.Context, source, dest string, sampleRate int) error
	ClickTrack(ctx context.Context, music, dest string, beats, downbeats []float64) error
}

// Analyze 分析已导入音乐的节拍，写出草稿网格与试听轨。
//
// 草稿永远不直接生效：模型会把重拍整体错位或判错拍号，只有人听过试听轨
// 才知道，所以必须再经 am beat confirm。
func Analyze(ctx context.Context, root string, env map[string]string, media Media, out io.Writer) (Grid, error) {
	config, err := LoadConfig(root)
	if err != nil {
		return Grid{}, err
	}
	track, err := LoadTrack(root)
	if err != nil {
		return Grid{}, fmt.Errorf("先用 am beat import 导入音乐：%w", err)
	}
	music, err := track.AudioPath(root)
	if err != nil {
		return Grid{}, err
	}
	wav, err := localPath(root, AnalysisFile)
	if err != nil {
		return Grid{}, err
	}
	if err := media.DecodeAnalysisWAV(ctx, music, wav, analysisSampleRate); err != nil {
		return Grid{}, err
	}
	result, err := localPath(root, Store+"/analysis.json")
	if err != nil {
		return Grid{}, err
	}
	_ = os.Remove(result)
	fmt.Fprintf(out, "正在用 beat-this %s（%s）分析节拍\n", AnalyzerVersion, config.Checkpoint)
	if err := runPython(ctx, config, env, wav, config.Checkpoint, result); err != nil {
		return Grid{}, err
	}
	var raw struct {
		Beats     []float64 `json:"beats"`
		Downbeats []float64 `json:"downbeats"`
	}
	if err := readJSON(result, &raw); err != nil {
		return Grid{}, err
	}
	grid := Grid{
		Schema: 1, AudioSHA: track.AudioSHA, Duration: track.Duration,
		Source:    fmt.Sprintf("beat-this/%s %s", AnalyzerVersion, config.Checkpoint),
		Beats:     within(raw.Beats, track.Duration),
		Downbeats: within(raw.Downbeats, track.Duration),
	}.normalize()
	if err := WriteDraftAndClick(ctx, root, grid, media); err != nil {
		return Grid{}, err
	}
	return grid, nil
}

// within 丢掉落在音乐时长之外的拍点。模型按 20 ms 一帧输出，最后一拍可能
// 正好落在结尾之后。
func within(values []float64, duration float64) []float64 {
	kept := make([]float64, 0, len(values))
	for _, v := range values {
		if v >= 0 && v < duration {
			kept = append(kept, v)
		}
	}
	return kept
}

// WriteDraftAndClick 写出草稿网格，并按它重新生成试听轨。
func WriteDraftAndClick(ctx context.Context, root string, g Grid, media Media) error {
	track, err := LoadTrack(root)
	if err != nil {
		return err
	}
	if err := g.checkTrack(track); err != nil {
		return err
	}
	if _, err := SaveDraft(root, g); err != nil {
		return err
	}
	return WriteClick(ctx, root, g, media)
}

// WriteClick 按给定网格生成试听轨。
func WriteClick(ctx context.Context, root string, g Grid, media Media) error {
	track, err := LoadTrack(root)
	if err != nil {
		return err
	}
	if err := g.checkTrack(track); err != nil {
		return err
	}
	music, err := track.AudioPath(root)
	if err != nil {
		return err
	}
	click, err := localPath(root, ClickFile)
	if err != nil {
		return err
	}
	return media.ClickTrack(ctx, music, click, g.Beats, g.Downbeats)
}

// Doctor 检查 Python 环境与固定版本，并预先下载模型权重。
func Doctor(ctx context.Context, root string, env map[string]string, out io.Writer) error {
	config, err := LoadConfig(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "检查 %s 中的 beat-this %s，首次运行会下载 %s 权重\n", config.Python, AnalyzerVersion, config.Checkpoint)
	if err := runPython(ctx, config, env, "--doctor", config.Checkpoint); err != nil {
		return err
	}
	fmt.Fprintln(out, "节拍分析环境可用")
	return nil
}

func runPython(ctx context.Context, config Config, env map[string]string, args ...string) error {
	binary, err := envutil.LookPath(config.Python, env["PATH"])
	if err != nil {
		return fmt.Errorf("找不到 Python（%s）：在独立环境里 pip install beat-this==%s 后，把 beat.yaml 的 python 指向该环境", config.Python, AnalyzerVersion)
	}
	cmd := exec.CommandContext(ctx, binary, append([]string{"-c", analyzeScript}, args...)...)
	cmd.Env = envutil.EnvList(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("节拍分析失败：%s；确认该环境装有 beat-this==%s", lastLine(stderr.String()), AnalyzerVersion)
	}
	return nil
}

// lastLine 取 stderr 最后一行有内容的输出；Python 的异常摘要在那里。
// 下载进度条用 \r 刷新同一行，也按行尾切开。
func lastLine(text string) string {
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "无错误输出"
}
