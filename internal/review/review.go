// Package review 对成片做整片复查：机器能判的音画时长、总帧数与镜头交界帧
// 直接给结论，其余交给联系表让人或编排工具逐页看。
//
// 镜头级的检查（scene.VerifyOutput、am scene frames）只看单镜头产物；拼接、
// 混音之后才出现的问题它们一个都看不到：音轨比画面短半秒、某镜在拼接时掉了
// 一帧、交界处闪一帧黑。这些要等成片发布后才被观众发现。
package review

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

// Prober 是复查用到的全部媒体操作；*mediaprobe.Toolchain 满足它。
type Prober interface {
	Probe(path string) (mediaprobe.Media, error)
	CountFrames(path string) (int, error)
	FrameStats(path string, atSeconds float64) (mediaprobe.FrameStats, error)
	ContactSheet(path string, atSeconds []float64, columns, thumbWidth int, dest string) error
}

// SeamFrames 是交界条带在切点两侧各取的帧数：30fps 下前后各 0.2 秒，正好覆盖
// 一次转场的收尾与起势。只看切点两侧各一帧，判断不了转场是否顺。
const SeamFrames = 6

// Segment 是成片里按顺序首尾相接的一个镜头。
type Segment struct {
	ID     string
	Frames int
}

// Options 控制一次复查。Segments 为空时只做与镜头无关的检查，联系表按固定间隔取帧。
type Options struct {
	Video    string
	FPS      int
	Segments []Segment
	OutDir   string
	// Silent 断言成片没有音轨（只拼接未混音的交付），不做音画比对。
	Silent bool
	// SyncTolerance 是音视频流时长允许的差值（秒）。
	SyncTolerance float64
	// Every 是没有镜头信息时联系表的取帧间隔（秒）。
	Every float64
	// TilesPerSheet 是每张联系表的格子数，超出就分页。
	TilesPerSheet int
	ThumbWidth    int
}

// Tile 是联系表里的一格。Position 是 start / middle / end / sample。
type Tile struct {
	SceneID   string  `json:"scene_id,omitempty"`
	Position  string  `json:"position"`
	Frame     int     `json:"frame"`
	AtSeconds float64 `json:"at_seconds"`
}

// Sheet 是一张联系表，Tiles 按图中从左到右、从上到下的顺序排列。
type Sheet struct {
	Image   string `json:"image"`
	Columns int    `json:"columns"`
	Tiles   []Tile `json:"tiles"`
}

// Cut 是两个镜头的交界：Before 是前一镜的末帧，After 是后一镜的首帧。
type Cut struct {
	From   string                `json:"from"`
	To     string                `json:"to"`
	Frame  int                   `json:"frame"`
	Before mediaprobe.FrameStats `json:"before"`
	After  mediaprobe.FrameStats `json:"after"`
}

// Report 是复查的机器可读结果。OK 只由 Problems 决定。
type Report struct {
	SchemaVersion  int              `json:"schema_version"`
	Path           string           `json:"path"`
	OK             bool             `json:"ok"`
	Media          mediaprobe.Media `json:"media"`
	ExpectedFrames int              `json:"expected_frames,omitempty"`
	CountedFrames  int              `json:"counted_frames,omitempty"`
	Problems       []string         `json:"problems"`
	Hints          []string         `json:"hints,omitempty"`
	Cuts           []Cut            `json:"cuts,omitempty"`
	Sheets         []Sheet          `json:"sheets"`
	// Seams 是每个镜头交界的逐帧条带，切点在第 SeamFrames+1 格。
	Seams []Sheet `json:"seams,omitempty"`
}

// ReportSchemaVersion 是 Report 的结构版本。
const ReportSchemaVersion = 1

// WriteJSON 原子写出报告。
func (r Report) WriteJSON(target string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(target, append(body, '\n'), 0o644)
}

// 缺省值。TilesPerSheet 取 3 的倍数，按镜头排版时一行正好是一个镜头的首、中、末。
const (
	DefaultSyncTolerance = 0.1
	DefaultEvery         = 5.0
	DefaultTilesPerSheet = 18
	DefaultThumbWidth    = 360
	segmentColumns       = 3
	sampleColumns        = 4
)

func (o Options) withDefaults() Options {
	if o.SyncTolerance <= 0 {
		o.SyncTolerance = DefaultSyncTolerance
	}
	if o.Every <= 0 {
		o.Every = DefaultEvery
	}
	if o.TilesPerSheet <= 0 {
		o.TilesPerSheet = DefaultTilesPerSheet
	}
	if o.ThumbWidth <= 0 {
		o.ThumbWidth = DefaultThumbWidth
	}
	return o
}

// Run 执行复查。error 只表示「检查没跑成」，「跑了但不合格」是 Report.OK == false。
func Run(p Prober, opts Options) (Report, error) {
	opts = opts.withDefaults()
	if opts.FPS <= 0 {
		return Report{}, fmt.Errorf("复查需要有效的帧率，收到 %d", opts.FPS)
	}
	for _, segment := range opts.Segments {
		if segment.Frames <= 0 {
			return Report{}, fmt.Errorf("镜头 %s 的帧数必须为正，收到 %d", segment.ID, segment.Frames)
		}
	}
	media, err := p.Probe(opts.Video)
	if err != nil {
		return Report{}, err
	}
	report := Report{SchemaVersion: ReportSchemaVersion, Path: opts.Video, Media: media, Problems: []string{}, Sheets: []Sheet{}}
	if media.Video == nil {
		report.Problems = append(report.Problems, "成片没有视频流")
		return report, nil
	}
	if opts.Silent {
		if media.Audio != nil {
			report.Problems = append(report.Problems, "按静音成片复查，但文件里有音轨")
		}
	} else {
		checkSync(&report, media, opts.SyncTolerance)
	}
	// 帧数总要实测：联系表的取帧上限只能信解码出的帧数。容器时长取的是最长
	// 的那条流，音轨比画面长时按它取帧会越过末帧，ffmpeg 返回成功却什么都不写。
	counted, err := p.CountFrames(opts.Video)
	if err != nil {
		return Report{}, err
	}
	report.CountedFrames = counted
	if len(opts.Segments) > 0 {
		checkFrames(&report, opts)
		if err := checkCuts(p, &report, opts); err != nil {
			return Report{}, err
		}
	}
	if err := writeSheets(p, &report, opts); err != nil {
		return Report{}, err
	}
	if err := writeSeams(p, &report, opts); err != nil {
		return Report{}, err
	}
	report.OK = len(report.Problems) == 0
	return report, nil
}

// checkSync 比较音视频流各自的时长。容器时长取的是较长的那条流，看不出差异。
func checkSync(report *Report, media mediaprobe.Media, tolerance float64) {
	if media.Audio == nil {
		report.Problems = append(report.Problems, "成片没有音轨")
		return
	}
	video, audio := media.Video.DurationSeconds, media.Audio.DurationSeconds
	if video <= 0 || audio <= 0 {
		report.Hints = append(report.Hints, "容器没有声明音视频流各自的时长，未做音画时长比对")
		return
	}
	if diff := math.Abs(video - audio); diff > tolerance {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"音画时长不一致：视频 %.3f 秒，音频 %.3f 秒，相差 %.3f 秒（容差 %.3f）",
			video, audio, diff, tolerance))
	}
}

// checkFrames 把精确解码出的总帧数与各镜头帧数之和比对。
// 不一致说明拼接时有镜头漏拼、重拼或掉帧，而容器声明的 nb_frames 不可信。
func checkFrames(report *Report, opts Options) {
	expected := 0
	for _, segment := range opts.Segments {
		expected += segment.Frames
	}
	counted := report.CountedFrames
	report.ExpectedFrames = expected
	if counted != expected {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"成片总帧数 %d 与镜头帧数之和 %d 不符（相差 %d 帧）：检查是否漏拼、重拼镜头或拼接掉帧",
			counted, expected, counted-expected))
	}
}

// checkCuts 取每个交界两侧各一帧做纯色判定。
//
// 结论只进 Hints：切黑、切白是合法的转场手法，机器分不清有意还是事故。
// 联系表会把这两帧放在相邻格子里，交给看图的一方判断。
func checkCuts(p Prober, report *Report, opts Options) error {
	cursor := 0
	for i := 0; i+1 < len(opts.Segments); i++ {
		cursor += opts.Segments[i].Frames
		// 成片比镜头合计短时（帧数检查已报问题），越过末尾的交界没有帧可取。
		if cursor >= report.CountedFrames {
			report.Hints = append(report.Hints, fmt.Sprintf(
				"成片只有 %d 帧，第 %d 帧起的镜头交界与联系表格子已跳过", report.CountedFrames, cursor))
			break
		}
		before, err := p.FrameStats(opts.Video, frameTime(cursor-1, opts.FPS))
		if err != nil {
			return err
		}
		after, err := p.FrameStats(opts.Video, frameTime(cursor, opts.FPS))
		if err != nil {
			return err
		}
		cut := Cut{From: opts.Segments[i].ID, To: opts.Segments[i+1].ID, Frame: cursor, Before: before, After: after}
		report.Cuts = append(report.Cuts, cut)
		if before.Blank() || after.Blank() {
			report.Hints = append(report.Hints, fmt.Sprintf(
				"%s → %s 交界（第 %d 帧）有纯色帧，确认是有意的转场而不是拼接事故", cut.From, cut.To, cut.Frame))
		}
	}
	return nil
}

func frameTime(frame, fps int) float64 { return float64(frame) / float64(fps) }

// planTiles 按镜头取首、中、末帧；没有镜头信息时按固定间隔取帧并补上末帧。
func planTiles(opts Options, counted int) ([]Tile, int) {
	var tiles []Tile
	if len(opts.Segments) > 0 {
		cursor := 0
		for _, segment := range opts.Segments {
			for _, spot := range []struct {
				position string
				frame    int
			}{{"start", cursor}, {"middle", cursor + segment.Frames/2}, {"end", cursor + segment.Frames - 1}} {
				tiles = append(tiles, Tile{SceneID: segment.ID, Position: spot.position, Frame: spot.frame, AtSeconds: frameTime(spot.frame, opts.FPS)})
			}
			cursor += segment.Frames
		}
		return tiles, segmentColumns
	}
	lastFrame := counted - 1
	step := max(1, int(math.Round(opts.Every*float64(opts.FPS))))
	for frame := 0; frame < lastFrame; frame += step {
		tiles = append(tiles, Tile{Position: "sample", Frame: frame, AtSeconds: frameTime(frame, opts.FPS)})
	}
	if lastFrame >= 0 {
		tiles = append(tiles, Tile{Position: "end", Frame: lastFrame, AtSeconds: frameTime(lastFrame, opts.FPS)})
	}
	return tiles, sampleColumns
}

// writeSheets 分页生成联系表。
func writeSheets(p Prober, report *Report, opts Options) error {
	tiles, columns := planTiles(opts, report.CountedFrames)
	kept := tiles[:0:0]
	for _, tile := range tiles {
		if tile.Frame < report.CountedFrames {
			kept = append(kept, tile)
		}
	}
	tiles = kept
	perSheet := max(columns, opts.TilesPerSheet/columns*columns)
	for start, page := 0, 1; start < len(tiles); start, page = start+perSheet, page+1 {
		chunk := tiles[start:min(start+perSheet, len(tiles))]
		image := filepath.Join(opts.OutDir, fmt.Sprintf("sheet-%02d.png", page))
		points := make([]float64, len(chunk))
		for i, tile := range chunk {
			points[i] = tile.AtSeconds
		}
		if err := p.ContactSheet(opts.Video, points, columns, opts.ThumbWidth, image); err != nil {
			return err
		}
		report.Sheets = append(report.Sheets, Sheet{Image: image, Columns: columns, Tiles: chunk})
	}
	return nil
}

// writeSeams 为每个落在成片范围内的交界出一张逐帧条带。
func writeSeams(p Prober, report *Report, opts Options) error {
	for i, cut := range report.Cuts {
		var tiles []Tile
		for frame := max(0, cut.Frame-SeamFrames); frame < min(report.CountedFrames, cut.Frame+SeamFrames); frame++ {
			owner, position := cut.From, "before"
			if frame >= cut.Frame {
				owner, position = cut.To, "after"
			}
			tiles = append(tiles, Tile{SceneID: owner, Position: position, Frame: frame, AtSeconds: frameTime(frame, opts.FPS)})
		}
		points := make([]float64, len(tiles))
		for j, tile := range tiles {
			points[j] = tile.AtSeconds
		}
		image := filepath.Join(opts.OutDir, fmt.Sprintf("seam-%02d.png", i+1))
		if err := p.ContactSheet(opts.Video, points, SeamFrames, opts.ThumbWidth, image); err != nil {
			return err
		}
		report.Seams = append(report.Seams, Sheet{Image: image, Columns: SeamFrames, Tiles: tiles})
	}
	return nil
}
