package beat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

// fourFour 是 120 BPM、4/4、弱起 0.5 秒的网格：第一个重拍在 0.5 秒，
// 共 n 小节，音乐在最后一小节结束后再响 tail 秒。
func fourFour(bars int, tail float64) Grid {
	g := Grid{Schema: 1, Source: "test"}
	g.Beats = append(g.Beats, 0.0) // 弱起拍
	for bar := 0; bar < bars; bar++ {
		for beat := 0; beat < 4; beat++ {
			at := 0.5 + float64(bar*4+beat)*0.5
			g.Beats = append(g.Beats, at)
			if beat == 0 {
				g.Downbeats = append(g.Downbeats, at)
			}
		}
	}
	g.Duration = 0.5 + float64(bars)*2 + tail
	return g
}

func TestGridProblems(t *testing.T) {
	if p := fourFour(4, 0.3).Problems(); len(p) != 0 {
		t.Fatalf("合格网格不应有问题：%v", p)
	}
	off := fourFour(4, 0.3)
	off.Downbeats[1] += 0.25
	if p := off.Problems(); len(p) != 1 || !strings.Contains(p[0], "没有落在任何普通拍上") {
		t.Fatalf("重拍不在拍上应报错：%v", p)
	}
	unsorted := fourFour(4, 0.3)
	unsorted.Beats[3], unsorted.Beats[4] = unsorted.Beats[4], unsorted.Beats[3]
	if p := unsorted.Problems(); len(p) == 0 || !strings.Contains(p[0], "beats") {
		t.Fatalf("倒序应报错：%v", p)
	}
	single := fourFour(1, 0.3)
	if p := single.Problems(); len(p) == 0 {
		t.Fatal("只有一个重拍无法切分，应报错")
	}
}

// 手改网格留下的毫秒级尾数在确认时对齐到普通拍。
func TestNormalizeSnapsDownbeatsToBeats(t *testing.T) {
	g := fourFour(3, 0)
	g.Downbeats[1] += 0.0015
	if got := g.normalize().Downbeats[1]; got != g.Beats[5] {
		t.Fatalf("重拍应对齐到 %.4f，实际 %.4f", g.Beats[5], got)
	}
}

func TestSummarizeAndBars(t *testing.T) {
	g := fourFour(6, 1.0)
	s := g.Summarize()
	if s.Bars != 6 || s.Meters[4] != 5 || s.PickupSeconds != 0.5 || s.BPM < 119.9 || s.BPM > 120.1 {
		t.Fatalf("概况不符：%+v", s)
	}
	bars := g.Bars()
	if bars[0].Number != 1 || bars[0].Start != 0.5 || bars[0].End != 2.5 || bars[5].End != g.Duration {
		t.Fatalf("小节边界不符：%+v", bars)
	}
}

func TestRegridShiftsDownbeatsOnly(t *testing.T) {
	g := fourFour(4, 0)
	g.Reviewed = true
	shifted, err := g.Regrid(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if shifted.Reviewed || len(shifted.Beats) != len(g.Beats) || shifted.Downbeats[0] != g.Beats[2] || shifted.Downbeats[1] != g.Beats[6] {
		t.Fatalf("重排结果不符：%+v", shifted)
	}
	if _, err := g.Regrid(3, 3); err == nil {
		t.Fatal("anchor 不小于 meter 应报错")
	}
	if _, err := g.Regrid(0, 0); err == nil {
		t.Fatal("meter 为 0 应报错")
	}
}

func TestLayoutCutsOnDownbeatFrames(t *testing.T) {
	g := fourFour(5, 0.4)
	plan := Plan{Schema: 1, Scenes: []PlannedShot{{"a", 2}, {"b", 1}, {"c", 2}}}
	segments, err := Layout(g, plan, 30)
	if err != nil {
		t.Fatal(err)
	}
	// 弱起并进第一个镜头；切点在第 3、4 小节的重拍（4.5 秒、6.5 秒）。
	want := []Segment{
		{ID: "a", FirstBar: 1, Bars: 2, StartFrame: 0, Frames: 135},
		{ID: "b", FirstBar: 3, Bars: 1, StartFrame: 135, Frames: 60},
		{ID: "c", FirstBar: 4, Bars: 2, StartFrame: 195, Frames: TotalFrames(g.Duration, 30) - 195},
	}
	for i := range want {
		if segments[i] != want[i] {
			t.Fatalf("第 %d 段不符：%+v，期望 %+v", i, segments[i], want[i])
		}
	}
	for _, bad := range []Plan{
		{Schema: 1, Scenes: []PlannedShot{{"a", 2}, {"b", 2}}},
		{Schema: 1, Scenes: []PlannedShot{{"a", 3}, {"a", 2}}},
		{Schema: 1, Scenes: []PlannedShot{{"a", 5}, {"b", 0}}},
		{Schema: 2, Scenes: []PlannedShot{{"a", 5}}},
	} {
		if _, err := Layout(g, bad, 30); err == nil {
			t.Errorf("应拒绝计划 %+v", bad)
		}
	}
}

func TestSliceNumbersBarsAndBeats(t *testing.T) {
	g := fourFour(3, 0)
	local := g.Slice(Segment{ID: "a", FirstBar: 1, Bars: 1, StartFrame: 0, Frames: 75}, 30)
	// 弱起拍（第 0 小节）+ 第 1 小节 4 拍；第 2 小节的重拍在第 75 帧，不属于本镜头。
	if len(local.Beats) != 5 {
		t.Fatalf("拍数不符：%+v", local.Beats)
	}
	if first := local.Beats[0]; first.Bar != 0 || first.Beat != 1 || first.Downbeat {
		t.Fatalf("弱起拍应记为第 0 小节：%+v", first)
	}
	if down := local.Beats[1]; down.Bar != 1 || down.Beat != 1 || !down.Downbeat || down.Frame != 15 {
		t.Fatalf("重拍不符：%+v", down)
	}
	second := g.Slice(Segment{ID: "b", FirstBar: 2, Bars: 1, StartFrame: 75, Frames: 60}, 30)
	if hit := second.Beats[0]; hit.Frame != 0 || hit.Bar != 2 || !hit.Downbeat || hit.Seconds != 0 {
		t.Fatalf("局部帧号应从镜头起点算：%+v", hit)
	}
}

type fakeMedia struct {
	media   mediaprobe.Media
	decodes int
	clicks  [][]float64
}

func (f *fakeMedia) Probe(string) (mediaprobe.Media, error) { return f.media, nil }
func (f *fakeMedia) Decode(string) error                    { f.decodes++; return nil }
func (f *fakeMedia) DecodeAnalysisWAV(context.Context, string, string, int) error {
	return nil
}
func (f *fakeMedia) ClickTrack(_ context.Context, _, dest string, beats, _ []float64) error {
	f.clicks = append(f.clicks, beats)
	return os.WriteFile(dest, []byte("wav"), 0o644)
}

func audioOnly(seconds float64) mediaprobe.Media {
	return mediaprobe.Media{DurationSeconds: seconds, Audio: &mediaprobe.AudioStream{DurationSeconds: seconds}}
}

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(DefaultConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeMusic(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportBindsAudioAndRefusesSilentReplace(t *testing.T) {
	root := newProject(t)
	media := &fakeMedia{media: audioOnly(10.5)}
	first := writeMusic(t, t.TempDir(), "Song.MP3", "first")
	track, err := Import(root, first, media, false)
	if err != nil {
		t.Fatal(err)
	}
	if track.Audio != "music-"+track.AudioSHA[:12]+".mp3" || track.Duration != 10.5 || track.Source != "Song.MP3" || media.decodes != 1 {
		t.Fatalf("导入记录不符：%+v", track)
	}
	if again, err := Import(root, first, media, false); err != nil || again != track {
		t.Fatalf("同一首重复导入应幂等：%+v, %v", again, err)
	}
	second := writeMusic(t, t.TempDir(), "other.wav", "second")
	if _, err := Import(root, second, media, false); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("换歌应要求 --replace：%v", err)
	}
	replaced, err := Import(root, second, media, true)
	if err != nil || filepath.Ext(replaced.Audio) != ".wav" {
		t.Fatalf("替换失败：%+v, %v", replaced, err)
	}
	if _, err := os.Stat(filepath.Join(root, Store, track.Audio)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("替换后旧音乐应被删除")
	}
	// 音乐被外部改动后，记录与文件对不上。
	os.WriteFile(filepath.Join(root, Store, replaced.Audio), []byte("tampered"), 0o644)
	if _, err := LoadTrack(root); err == nil {
		t.Fatal("音乐被改动应报错")
	}
}

func TestImportRejectsVideoAndUnknownFormats(t *testing.T) {
	root := newProject(t)
	video := &fakeMedia{media: mediaprobe.Media{DurationSeconds: 3, Audio: &mediaprobe.AudioStream{}, Video: &mediaprobe.VideoStream{}}}
	if _, err := Import(root, writeMusic(t, t.TempDir(), "a.mp3", "x"), video, false); err == nil {
		t.Fatal("带视频流的文件应拒绝")
	}
	if _, err := Import(root, writeMusic(t, t.TempDir(), "a.txt", "x"), &fakeMedia{media: audioOnly(3)}, false); err == nil {
		t.Fatal("未知格式应拒绝")
	}
}

func TestConfigRejectsCoexistingModes(t *testing.T) {
	root := newProject(t)
	os.WriteFile(filepath.Join(root, "song.yaml"), []byte("schema: 1\n"), 0o644)
	if _, err := LoadConfig(root); err == nil || !strings.Contains(err.Error(), "song.yaml") {
		t.Fatalf("与歌曲模式共存应报错：%v", err)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, ConfigFile), []byte("schema: 1\ncheckpoint: huge\npython: python3\n"), 0o644)
	if _, err := LoadConfig(other); err == nil {
		t.Fatal("未知模型应报错")
	}
}

func importedProject(t *testing.T, g Grid) (string, Grid) {
	t.Helper()
	root := newProject(t)
	track, err := Import(root, writeMusic(t, t.TempDir(), "m.wav", "music"), &fakeMedia{media: audioOnly(g.Duration)}, false)
	if err != nil {
		t.Fatal(err)
	}
	g.AudioSHA = track.AudioSHA
	return root, g
}

func TestConfirmIsTheOnlyWayToAReviewedGrid(t *testing.T) {
	root, g := importedProject(t, fourFour(4, 0.2))
	media := &fakeMedia{}
	if err := WriteDraftAndClick(context.Background(), root, g, media); err != nil {
		t.Fatal(err)
	}
	if len(media.clicks) != 1 {
		t.Fatal("写草稿时应同时生成试听轨")
	}
	if _, err := LoadGrid(root); err == nil {
		t.Fatal("只有草稿时不应读到正式网格")
	}
	draft, err := ReadDraft(filepath.Join(root, DraftFile))
	if err != nil || draft.Reviewed {
		t.Fatalf("草稿必须是未确认状态：%+v, %v", draft, err)
	}
	if _, err := Confirm(root, draft); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGrid(root)
	if err != nil || !loaded.Reviewed {
		t.Fatalf("确认后应能读到正式网格：%+v, %v", loaded, err)
	}
	foreign := draft
	foreign.AudioSHA = "other"
	if _, err := Confirm(root, foreign); err == nil {
		t.Fatal("属于另一首音乐的网格不能确认")
	}
	broken := draft
	broken.Downbeats = broken.Downbeats[:1]
	if _, err := Confirm(root, broken); err == nil {
		t.Fatal("不合格网格不能确认")
	}
}

func TestLastLineSkipsProgressNoise(t *testing.T) {
	if got := lastLine("0.1%\r50%\r100%\nTraceback\nValueError: bad\n\n"); got != "ValueError: bad" {
		t.Fatalf("应取最后一行有内容的输出：%q", got)
	}
}
