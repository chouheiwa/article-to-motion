package mediaprobe

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// PrepareAlignment preserves time while decoding a mono 16 kHz analysis copy.
func (t Toolchain) PrepareAlignment(ctx context.Context, source, dest string) error {
	return t.songCommand(ctx, dest, "-i", source, "-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le")
}
func (t Toolchain) songCommand(ctx context.Context, dest string, args ...string) error {
	binary, e := t.ffmpegPath()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(filepath.Dir(dest), ".song-*"+filepath.Ext(dest))
	if e != nil {
		return e
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)
	argv := append([]string{"-v", "error", "-nostdin", "-y"}, args...)
	argv = append(argv, name)
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Env = t.env
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("歌曲媒体处理失败：%s", commandError(e))
	}
	return os.Rename(name, dest)
}

// songAACBitrate 显式给出，不吃 ffmpeg 原生 aac 的缺省 128k：瞬态密集的音乐在
// 低码率下编解码一趟，采样之间的峰值能抬高 3 dB 以上，波形文件达标的混音在
// 成片里顶过 0 dBFS。am review 量的是解码后的真峰，会把这种情况报出来。
const songAACBitrate = "384k"

// MuxSong accepts an exact-frame silent master. Only the sub-frame tail is
// padded; no video scaling, retiming, BGM or speech sidechain is available.
func (t Toolchain) MuxSong(ctx context.Context, video, audio, dest string, duration float64, fps int) error {
	if fps <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return fmt.Errorf("歌曲时长或帧率无效")
	}
	frames := int(math.Ceil(duration*float64(fps) - 1e-9))
	target := float64(frames) / float64(fps)
	for _, p := range []string{video, audio} {
		a, _ := filepath.Abs(p)
		b, _ := filepath.Abs(dest)
		if a == b {
			return fmt.Errorf("输出不得覆盖输入")
		}
	}
	m, e := t.Probe(audio)
	if e != nil {
		return e
	}
	if m.Audio == nil || m.Video != nil || math.Abs(m.DurationSeconds-duration) > 1e-6 {
		return fmt.Errorf("选定歌曲音频规格或时长不一致")
	}
	report, e := t.Verify(video, VerifyOptions{Spec: Spec{FPS: fps, Frames: frames, DurationSeconds: target, Tolerance: 0.001, Audio: AudioAbsent}, ExactFrames: true})
	if e != nil {
		return e
	}
	if !report.OK {
		return fmt.Errorf("歌曲静音母版不合格：%v", report.Problems)
	}
	filter := "apad=pad_dur=" + strconv.FormatFloat(math.Max(0, target-duration), 'f', 9, 64) + ",atrim=duration=" + strconv.FormatFloat(target, 'f', 9, 64)
	if e = t.songCommand(ctx, dest, "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-af", filter, "-c:a", "aac", "-b:a", songAACBitrate, "-ar", "48000", "-ac", "2", "-movflags", "+faststart"); e != nil {
		return e
	}
	report, e = t.Verify(dest, VerifyOptions{Spec: Spec{FPS: fps, Frames: frames, DurationSeconds: target, Tolerance: 1.0 / float64(fps), Audio: AudioPresent, SampleRate: 48000, Channels: 2}, ExactFrames: true, Decode: true})
	if e != nil {
		return e
	}
	if !report.OK {
		return fmt.Errorf("歌曲成片不合格：%v", report.Problems)
	}
	return nil
}
