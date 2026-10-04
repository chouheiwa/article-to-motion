package mediaprobe

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// DecodeAnalysisWAV 把音乐解码成单声道 16 位 WAV，供节拍分析读取。
//
// 分析端只用 Python 标准库的 wave 读取，不走分析库自带的 torchaudio 读文件：
// 后者的后端随版本变化，MP3/AAC 能不能读取决于用户机器上装了什么。
func (t Toolchain) DecodeAnalysisWAV(ctx context.Context, source, dest string, sampleRate int) error {
	if sampleRate <= 0 {
		return fmt.Errorf("分析采样率必须为正，收到 %d", sampleRate)
	}
	return t.transcode(ctx, dest, "-i", source, "-vn", "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-c:a", "pcm_s16le")
}

// clickSampleRate 是试听轨的采样率；与成片工作格式一致。
const clickSampleRate = 48000

// 咔嗒声的音色：重拍高八度，人耳一听就能分清每小节从哪开始。
const (
	clickSeconds       = 0.035
	clickDownbeatHz    = 1760.0
	clickBeatHz        = 880.0
	clickDownbeatLevel = 0.55
	clickBeatLevel     = 0.30
	clickMusicLevel    = 0.5
)

// ClickTrack 把音乐压低 6 dB，叠上每拍一声、重拍更高更响的咔嗒声，写成 WAV。
//
// 它只服务人工试听：节拍网格对不对，看数字判断不了，听一遍立刻知道。
// 咔嗒声落在网格给的时刻上，所以「咔嗒与鼓点同时」就是网格正确的定义。
// 整首解码进内存（约 0.77 MB/秒），按短视频配乐的长度设计，不面向小时级音频。
func (t Toolchain) ClickTrack(ctx context.Context, music, dest string, beats, downbeats []float64) error {
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-i", music, "-vn",
		"-ac", "2", "-ar", strconv.Itoa(clickSampleRate), "-f", "s16le", "-acodec", "pcm_s16le", "-")
	cmd.Env = t.env
	pcm, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("无法解码音乐：%s（%s）", music, commandError(err))
	}
	frames := len(pcm) / 4
	mix := make([]float64, frames*2)
	for i := range mix {
		mix[i] = float64(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768 * clickMusicLevel
	}
	for _, b := range beats {
		hz, level := clickBeatHz, clickBeatLevel
		if nearAny(b, downbeats, clickMatchSeconds) {
			hz, level = clickDownbeatHz, clickDownbeatLevel
		}
		addClick(mix, int(math.Round(b*clickSampleRate)), hz, level)
	}
	return writeWAV(dest, mix, clickSampleRate, 2)
}

// clickMatchSeconds 是判定「这一拍是重拍」的时间容差。手改过的网格里重拍与
// 普通拍可能差一两毫秒的浮点尾数，按精确相等比会把重拍听成普通拍。
const clickMatchSeconds = 0.002

func nearAny(value float64, sorted []float64, tolerance float64) bool {
	for _, candidate := range sorted {
		if math.Abs(candidate-value) <= tolerance {
			return true
		}
	}
	return false
}

func addClick(mix []float64, start int, hz, level float64) {
	length := int(clickSeconds * clickSampleRate)
	for i := 0; i < length; i++ {
		frame := start + i
		if frame < 0 || 2*frame+1 >= len(mix) {
			continue
		}
		t := float64(i) / clickSampleRate
		value := level * math.Sin(2*math.Pi*hz*t) * math.Exp(-t/(clickSeconds/5))
		mix[2*frame] += value
		mix[2*frame+1] += value
	}
}

// writeWAV 原子写出 16 位 PCM WAV。超出 ±1 的采样用 tanh 软限幅，不硬削波。
func writeWAV(dest string, samples []float64, sampleRate, channels int) error {
	data := make([]byte, 44+2*len(samples))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(samples)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], uint16(channels))
	binary.LittleEndian.PutUint32(data[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(data[28:], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(data[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(samples)))
	for i, s := range samples {
		if math.Abs(s) > 0.9 {
			s = math.Copysign(0.9+0.1*math.Tanh((math.Abs(s)-0.9)/0.1), s)
		}
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(math.Round(s*32767))))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(dest, data, 0o644)
}
