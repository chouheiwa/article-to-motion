package mediaprobe

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContactSheetTilesFramesIntoOneImage：5 帧按 3 列拼成 2 行，最后一行不满
// 也要输出。尺寸 = 列数 × 缩略图宽 + 分隔，按公式核对，不靠肉眼。
func TestContactSheetTilesFramesIntoOneImage(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=2")
	dest := filepath.Join(t.TempDir(), "review", "sheet.png")
	if err := tc.ContactSheet(path, []float64{0, 0.5, 1, 1.5, 1.9}, 3, 64, dest); err != nil {
		t.Fatalf("ContactSheet 失败：%v", err)
	}
	media, err := tc.Probe(dest)
	if err != nil {
		t.Fatal(err)
	}
	wantWidth := 3*64 + 2*contactSheetPadding + 2*contactSheetPadding
	wantHeight := 2*48 + contactSheetPadding + 2*contactSheetPadding
	if media.Video.WidthPx != wantWidth || media.Video.HeightPx != wantHeight {
		t.Fatalf("联系表尺寸 = %s，期望 %dx%d", media.Video.Resolution(), wantWidth, wantHeight)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".contact-*"))
	if len(leftovers) != 0 {
		t.Fatalf("临时缩略图目录没有清理：%v", leftovers)
	}
}

func TestContactSheetRejectsBadInput(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=1")
	dest := filepath.Join(t.TempDir(), "sheet.png")
	if err := tc.ContactSheet(path, nil, 3, 64, dest); err == nil {
		t.Error("没有时间点时应报错")
	}
	if err := tc.ContactSheet(path, []float64{0}, 0, 64, dest); err == nil {
		t.Error("列数为 0 时应报错")
	}
	if err := tc.ContactSheet(path, []float64{5}, 3, 64, dest); err == nil {
		t.Error("时间点超出时长时应报错而不是产出空图")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("失败时不应留下联系表文件")
	}
}

// TestInputSeekIsFrameAccurate：FrameStats 改为输入端定位后必须仍取到第 k 帧。
// 素材每帧亮度 = 帧号 × 4，GOP 设为 120，让取样点跨过关键帧。
func TestInputSeekIsFrameAccurate(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "color=c=black:s=64x64:r=30:d=6,format=gray,geq=lum='mod(N\\,60)*4'", "-g", "120", "-qp", "0")
	for _, frame := range []int{2, 7, 59, 119, 130, 179} {
		stats, err := tc.FrameStats(path, float64(frame)/30)
		if err != nil {
			t.Fatal(err)
		}
		want := float64((frame % 60) * 4)
		if stats.Mean < want-2 || stats.Mean > want+2 {
			t.Errorf("第 %d 帧亮度 %.1f，期望约 %.0f", frame, stats.Mean, want)
		}
	}
}
