package mediaprobe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// contactSheetPadding 与 contactSheetBackground 让缩略图之间留出可辨认的分隔，
// 否则深色风格的相邻两帧会连成一片，看不出镜头交界。
const (
	contactSheetPadding    = 8
	contactSheetBackground = "0x303030"
)

// ContactSheet 把 atSeconds 处的帧缩成 thumbWidth 宽的缩略图，按 columns 列
// 拼成一张 PNG 写到 dest。缩略图按传入顺序从左到右、从上到下排列。
//
// 不在图上叠时间戳文字：drawtext 依赖 ffmpeg 编译时带 freetype，发行版之间
// 不一致，缺了就整条命令失败。格子与时间点的对应关系由调用方写进报告。
func (t Toolchain) ContactSheet(path string, atSeconds []float64, columns, thumbWidth int, dest string) error {
	if len(atSeconds) == 0 {
		return fmt.Errorf("联系表至少需要一个时间点")
	}
	if columns < 1 || thumbWidth < 16 {
		return fmt.Errorf("联系表列数必须 ≥ 1、缩略图宽度必须 ≥ 16，收到 %d 列、%d 像素", columns, thumbWidth)
	}
	ffmpeg, err := t.ffmpegPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp(filepath.Dir(dest), ".contact-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)
	for i, at := range atSeconds {
		thumb := filepath.Join(workDir, fmt.Sprintf("thumb-%04d.png", i+1))
		// 宽度固定、高度按比例且取偶数，同一张联系表里的格子尺寸一致。
		// -ss 放在 -i 前面：转码时 ffmpeg 默认精确定位（accurate_seek），取到的帧
		// 与放在后面逐字节相同，但不必每格都从片头解码，长片的联系表快一个量级。
		cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-ss", formatSeconds(at), "-i", path,
			"-frames:v", "1",
			"-vf", fmt.Sprintf("scale=%d:-2", thumbWidth), "-f", "image2", "-c:v", "png", thumb)
		cmd.Env = t.env
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("联系表抽帧失败：%s @ %.3f 秒（%s）", path, at, firstLine(string(output)))
		}
		if info, statErr := os.Stat(thumb); statErr != nil || info.Size() == 0 {
			return fmt.Errorf("联系表抽帧没有产出内容：%s @ %.3f 秒（时间点可能超出视频时长）", path, at)
		}
	}
	rows := (len(atSeconds) + columns - 1) / columns
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-framerate", "1", "-start_number", "1",
		"-i", filepath.Join(workDir, "thumb-%04d.png"),
		"-vf", fmt.Sprintf("tile=%dx%d:padding=%d:margin=%d:color=%s",
			columns, rows, contactSheetPadding, contactSheetPadding, contactSheetBackground),
		"-frames:v", "1", "-f", "image2", "-c:v", "png", dest)
	cmd.Env = t.env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("拼接联系表失败：%s（%s）", dest, firstLine(string(output)))
	}
	if info, err := os.Stat(dest); err != nil || info.Size() == 0 {
		return fmt.Errorf("联系表没有产出内容：%s", dest)
	}
	return nil
}
