package mediaprobe

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// loudnessSampleRate 是响度采样前统一重采样到的采样率。语音能量集中在 4kHz
// 以下，16kHz 足够，也让五分钟配音的 PCM 只有十来 MB。
const loudnessSampleRate = 16000

// SilenceFloorDB 是全静音窗口的电平。数字静音的真实值是负无穷，
// 用一个有限值代替，让下游比较与 JSON 序列化都不必特判。
const SilenceFloorDB = -120.0

// Loudness 把音频解码成 16kHz 单声道 16 位 PCM，按 windowSeconds 切窗，
// 返回每窗的 RMS 电平（dBFS）。
//
// 不用 ffmpeg 的 silencedetect：它把结果写进 stderr 的文本日志，格式随版本
// 变化，而且阈值在命令行里写死，改判据就得重跑一遍解码。裸 PCM 是稳定接口，
// 判据留给调用方——与 FrameStats 取原始灰度像素是同一个取舍。
func (t Toolchain) Loudness(path string, windowSeconds float64) ([]float64, error) {
	if windowSeconds <= 0 || math.IsNaN(windowSeconds) || math.IsInf(windowSeconds, 0) {
		return nil, fmt.Errorf("响度窗口必须是正数，收到 %v", windowSeconds)
	}
	window := int(math.Round(windowSeconds * loudnessSampleRate))
	if window < 1 {
		return nil, fmt.Errorf("响度窗口太短：%v 秒", windowSeconds)
	}
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", path, "-vn",
		"-ac", "1", "-ar", fmt.Sprint(loudnessSampleRate), "-f", "s16le", "-acodec", "pcm_s16le", "-")
	cmd.Env = t.env
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("无法解码音频：%s（%s）", path, commandError(err))
	}
	samples := len(output) / 2
	if samples == 0 {
		return nil, fmt.Errorf("音频里没有可解码的采样：%s", path)
	}
	levels := make([]float64, 0, (samples+window-1)/window)
	for start := 0; start < samples; start += window {
		end := min(start+window, samples)
		var sum float64
		for i := start; i < end; i++ {
			value := float64(int16(binary.LittleEndian.Uint16(output[2*i:]))) / 32768
			sum += value * value
		}
		levels = append(levels, rmsToDB(math.Sqrt(sum/float64(end-start))))
	}
	return levels, nil
}

func rmsToDB(rms float64) float64 {
	if rms <= 0 {
		return SilenceFloorDB
	}
	return max(SilenceFloorDB, 20*math.Log10(rms))
}

// ProgramLoudness 是整段节目的 EBU R128 实测值，取自解码后的音频。
type ProgramLoudness struct {
	IntegratedLUFS float64 `json:"integrated_lufs"`
	TruePeakDBTP   float64 `json:"true_peak_dbtp"`
	RangeLU        float64 `json:"range_lu"`
}

// MeasureProgramLoudness 用 loudnorm 的测量通道量积分响度、真峰与响度范围。
//
// 量的必须是编码后的成片，不是混音导出的 WAV：AAC 会把采样之间的峰值再抬高，
// 波形文件 -2.5 dBTP 的素材编解码一趟可以到 0 dBFS 以上。loudnorm 的真峰做了
// 过采样，正是要查的那个数。
//
// 结果从 stderr 里的 JSON 块读，不读 ebur128 的文本摘要：JSON 的字段名是
// loudnorm 两遍规范化的输入契约，比随版本变化的日志排版稳定。全静音时 ffmpeg
// 给 "-inf"，按 SilenceFloorDB 记，让报告仍能写成 JSON。
func (t Toolchain) MeasureProgramLoudness(path string) (ProgramLoudness, error) {
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return ProgramLoudness{}, err
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-nostats", "-nostdin", "-i", path, "-vn",
		"-af", "loudnorm=print_format=json", "-f", "null", "-")
	cmd.Env = t.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ProgramLoudness{}, fmt.Errorf("无法测量响度：%s（%s）", path, commandError(err))
	}
	return parseLoudnorm(stderr.String())
}

func parseLoudnorm(log string) (ProgramLoudness, error) {
	start, end := strings.LastIndex(log, "{"), strings.LastIndex(log, "}")
	if start < 0 || end < start {
		return ProgramLoudness{}, fmt.Errorf("loudnorm 没有输出测量结果")
	}
	var raw struct {
		I   string `json:"input_i"`
		TP  string `json:"input_tp"`
		LRA string `json:"input_lra"`
	}
	if err := json.Unmarshal([]byte(log[start:end+1]), &raw); err != nil {
		return ProgramLoudness{}, fmt.Errorf("loudnorm 测量结果无法解析：%w", err)
	}
	values := make([]float64, 3)
	for i, field := range []string{raw.I, raw.TP, raw.LRA} {
		value, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || math.IsNaN(value) {
			return ProgramLoudness{}, fmt.Errorf("loudnorm 测量值 %q 不是数字", field)
		}
		values[i] = max(SilenceFloorDB, value)
	}
	return ProgramLoudness{IntegratedLUFS: values[0], TruePeakDBTP: values[1], RangeLU: values[2]}, nil
}
