package beat

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// Grid 是一首音乐的节拍网格：每一拍的时刻，以及其中哪些是小节第一拍（重拍）。
//
// 重拍必须是普通拍的子集：拍内分工（第几拍做什么）要靠「这一拍在小节里排第几」，
// 重拍不落在某个普通拍上就数不出来。
type Grid struct {
	Schema    int       `json:"schema"`
	AudioSHA  string    `json:"audio_sha256"`
	Duration  float64   `json:"duration_seconds"`
	Source    string    `json:"source"`
	Reviewed  bool      `json:"reviewed"`
	Beats     []float64 `json:"beats"`
	Downbeats []float64 `json:"downbeats"`
}

// downbeatMatch 是重拍对齐到普通拍的容差。分析模型的时间分辨率是 20 ms，
// 手改网格时浮点尾数会有毫秒级出入，再大就是真的没落在拍上。
const downbeatMatch = 0.002

// Problems 返回网格的全部不合格项。
func (g Grid) Problems() []string {
	var problems []string
	if g.Schema != 1 || !finite(g.Duration) || g.Duration <= 0 {
		problems = append(problems, "网格版本或时长无效")
	}
	problems = append(problems, increasing("beats", g.Beats, g.Duration)...)
	problems = append(problems, increasing("downbeats", g.Downbeats, g.Duration)...)
	if len(g.Downbeats) < 2 {
		problems = append(problems, "重拍少于两个，无法按小节切分")
	}
	for _, d := range g.Downbeats {
		if _, ok := nearestBeat(g.Beats, d); !ok {
			problems = append(problems, fmt.Sprintf("重拍 %.3f 秒没有落在任何普通拍上", d))
		}
	}
	return problems
}

func increasing(field string, values []float64, duration float64) []string {
	last := math.Inf(-1)
	for i, v := range values {
		if !finite(v) || v < 0 || v >= duration || v <= last {
			return []string{fmt.Sprintf("%s 第 %d 项（%.3f）倒序、重复或越界", field, i+1, v)}
		}
		last = v
	}
	return nil
}

func nearestBeat(beats []float64, at float64) (int, bool) {
	i := sort.SearchFloat64s(beats, at-downbeatMatch)
	if i < len(beats) && math.Abs(beats[i]-at) <= downbeatMatch {
		return i, true
	}
	return -1, false
}

// normalize 把重拍对齐到它所落的那个普通拍的精确值，之后两者可按相等比较。
func (g Grid) normalize() Grid {
	out := g
	out.Downbeats = make([]float64, 0, len(g.Downbeats))
	for _, d := range g.Downbeats {
		if i, ok := nearestBeat(g.Beats, d); ok {
			out.Downbeats = append(out.Downbeats, g.Beats[i])
		} else {
			out.Downbeats = append(out.Downbeats, d)
		}
	}
	return out
}

// Bar 是一个小节：从一个重拍到下一个重拍（最后一小节到音乐结束）。
type Bar struct {
	Number int     `json:"number"`
	Start  float64 `json:"start_seconds"`
	End    float64 `json:"end_seconds"`
	Beats  int     `json:"beats"`
}

// Bars 按重拍切出全部小节，编号从 1 开始。第一个重拍之前的弱起不算小节，
// 切镜头时并进第一个镜头。
func (g Grid) Bars() []Bar {
	bars := make([]Bar, 0, len(g.Downbeats))
	for i, start := range g.Downbeats {
		end := g.Duration
		if i+1 < len(g.Downbeats) {
			end = g.Downbeats[i+1]
		}
		count := 0
		for _, b := range g.Beats {
			if b >= start-downbeatMatch && b < end-downbeatMatch {
				count++
			}
		}
		bars = append(bars, Bar{Number: i + 1, Start: start, End: end, Beats: count})
	}
	return bars
}

// Summary 是给人看的网格概况，用来在试听之前先发现明显的错误。
type Summary struct {
	Bars          int
	BPM           float64
	Meters        map[int]int
	PickupSeconds float64
}

// Summarize 统计速度、拍号分布与弱起。最后一小节常被音乐结尾截断，不计入拍号。
func (g Grid) Summarize() Summary {
	s := Summary{Meters: map[int]int{}}
	bars := g.Bars()
	s.Bars = len(bars)
	for i, bar := range bars {
		if i < len(bars)-1 {
			s.Meters[bar.Beats]++
		}
	}
	if len(g.Downbeats) > 0 {
		s.PickupSeconds = g.Downbeats[0]
	}
	if len(g.Beats) > 1 {
		intervals := make([]float64, 0, len(g.Beats)-1)
		for i := 1; i < len(g.Beats); i++ {
			intervals = append(intervals, g.Beats[i]-g.Beats[i-1])
		}
		sort.Float64s(intervals)
		s.BPM = 60 / intervals[len(intervals)/2]
	}
	return s
}

// String 渲染成一行中文概况。
func (s Summary) String() string {
	meters := make([]int, 0, len(s.Meters))
	for m := range s.Meters {
		meters = append(meters, m)
	}
	sort.Ints(meters)
	parts := make([]string, 0, len(meters))
	for _, m := range meters {
		parts = append(parts, fmt.Sprintf("%d 拍 × %d", m, s.Meters[m]))
	}
	return fmt.Sprintf("%d 小节，约 %.1f BPM，拍号分布 [%s]，弱起 %.3f 秒", s.Bars, s.BPM, strings.Join(parts, "，"), s.PickupSeconds)
}

// Regrid 按固定拍号重排重拍：从第 anchor 拍（从 0 数）起每 meter 拍一个重拍。
//
// 分析模型最常见的错误是重拍整体错位（把第 3 拍当成小节开头）或拍号判错，
// 普通拍本身通常是对的。手改 JSON 里几十个时间戳既慢又容易错，这个操作
// 只换「哪些拍是重拍」，普通拍原样保留。结果是未确认的草稿。
func (g Grid) Regrid(meter, anchor int) (Grid, error) {
	if meter < 1 || meter > 16 {
		return Grid{}, fmt.Errorf("拍号必须在 1..16 之间，收到 %d", meter)
	}
	if anchor < 0 || anchor >= meter || anchor >= len(g.Beats) {
		return Grid{}, fmt.Errorf("起始拍必须在 0..%d 之间且不超过总拍数，收到 %d", meter-1, anchor)
	}
	out := g
	out.Reviewed = false
	out.Downbeats = nil
	for i := anchor; i < len(g.Beats); i += meter {
		out.Downbeats = append(out.Downbeats, g.Beats[i])
	}
	out.Source = strings.TrimSuffix(g.Source, " +regrid") + " +regrid"
	return out, nil
}

// checkTrack 确认网格属于当前导入的音乐。
func (g Grid) checkTrack(track Track) error {
	if g.AudioSHA != track.AudioSHA || math.Abs(g.Duration-track.Duration) > 1e-6 {
		return fmt.Errorf("节拍网格属于另一首音乐或另一个版本，请重新 am beat analyze")
	}
	return nil
}

// ReadDraft 读取任意路径的网格文件（通常是手改过的草稿），不要求已确认。
func ReadDraft(path string) (Grid, error) {
	var g Grid
	if err := readJSON(path, &g); err != nil {
		return g, err
	}
	return g, nil
}

// SaveDraft 写出未确认的网格草稿。不合格的草稿照样写出，便于人工修正。
func SaveDraft(root string, g Grid) (string, error) {
	g.Reviewed = false
	path, err := localPath(root, DraftFile)
	if err != nil {
		return "", err
	}
	return path, writeJSON(path, g)
}

// Confirm 把人工核对过的网格写成正式网格。只有这一步能把 reviewed 置为 true。
func Confirm(root string, g Grid) (Grid, error) {
	track, err := LoadTrack(root)
	if err != nil {
		return Grid{}, err
	}
	if err := g.checkTrack(track); err != nil {
		return Grid{}, err
	}
	if problems := g.Problems(); len(problems) > 0 {
		return Grid{}, fmt.Errorf("网格不合格：%s", strings.Join(problems, "；"))
	}
	confirmed := g.normalize()
	confirmed.Reviewed = true
	path, err := localPath(root, GridFile)
	if err != nil {
		return Grid{}, err
	}
	return confirmed, writeJSON(path, confirmed)
}

// LoadGrid 读取已确认的正式网格，并核对它仍属于当前音乐。
//
// reviewed 只是一个字段：它防的是「拿草稿直接切镜头」这种疏忽，不防有人
// 故意手改 grid.json。确认之后的任何改动都会改变文件摘要，已写好的镜头 cue
// 随之失效，必须重跑 am beat cues。
func LoadGrid(root string) (Grid, error) {
	var g Grid
	if _, err := LoadConfig(root); err != nil {
		return g, err
	}
	track, err := LoadTrack(root)
	if err != nil {
		return g, err
	}
	path, err := localPath(root, GridFile)
	if err != nil {
		return g, err
	}
	if _, err := os.Stat(path); err != nil {
		return g, fmt.Errorf("还没有已确认的节拍网格：先 am beat analyze，试听后 am beat confirm")
	}
	if err := readJSON(path, &g); err != nil {
		return g, err
	}
	if !g.Reviewed {
		return g, fmt.Errorf("%s 未经人工确认，请用 am beat confirm 导入", GridFile)
	}
	if err := g.checkTrack(track); err != nil {
		return g, err
	}
	if problems := g.Problems(); len(problems) > 0 {
		return g, fmt.Errorf("网格不合格：%s", strings.Join(problems, "；"))
	}
	return g, nil
}

// FrameOf 是全片唯一的「秒 → 帧」换算。切点、拍点帧号都走它。
func FrameOf(seconds float64, fps int) int {
	return int(math.Round(seconds * float64(fps)))
}

// TotalFrames 是覆盖整首音乐所需的帧数：最后不足一帧也要占一帧，与成片封装一致。
func TotalFrames(duration float64, fps int) int {
	return int(math.Ceil(duration*float64(fps) - 1e-9))
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
