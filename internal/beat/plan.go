package beat

import (
	"fmt"
	"os"
	"strings"
)

// Plan 是按小节写的分镜：每个镜头占连续几个小节。镜头时长只能这样表达，
// 不接受秒数——写秒数就有可能写出不在重拍上的切点。
type Plan struct {
	Schema int           `json:"schema"`
	Scenes []PlannedShot `json:"scenes"`
}

// PlannedShot 是计划里的一个镜头。
type PlannedShot struct {
	ID   string `json:"id"`
	Bars int    `json:"bars"`
}

// Segment 是换算到帧之后的镜头：成片里从 StartFrame 起共 Frames 帧，
// 覆盖第 FirstBar 起的 Bars 个小节（小节编号从 1 开始）。
type Segment struct {
	ID         string
	FirstBar   int
	Bars       int
	StartFrame int
	Frames     int
}

// LoadPlan 读取分镜计划。
func LoadPlan(root string) (Plan, error) {
	var p Plan
	path, err := localPath(root, PlanFile)
	if err != nil {
		return p, err
	}
	if _, err := os.Stat(path); err != nil {
		return p, fmt.Errorf("还没有分镜计划 %s：按小节为每个镜头写 {\"id\", \"bars\"}", PlanFile)
	}
	if err := readJSON(path, &p); err != nil {
		return p, err
	}
	return p, nil
}

// Layout 把分镜计划换算成整数帧的镜头区间。
//
// 第一个重拍之前的弱起并进第一个镜头，最后一小节延伸到音乐结束；除此之外
// 每个切点都是某个重拍的帧号。小节总数必须与网格一致：少了是音乐末尾没有
// 画面，多了是画面超出音乐。
func Layout(g Grid, p Plan, fps int) ([]Segment, error) {
	if fps <= 0 {
		return nil, fmt.Errorf("帧率必须为正，收到 %d", fps)
	}
	if p.Schema != 1 || len(p.Scenes) == 0 {
		return nil, fmt.Errorf("%s：schema 必须为 1 且至少有一个镜头", PlanFile)
	}
	seen := map[string]bool{}
	total := 0
	for _, shot := range p.Scenes {
		if strings.TrimSpace(shot.ID) == "" || seen[shot.ID] {
			return nil, fmt.Errorf("%s：镜头 id 缺失或重复：%q", PlanFile, shot.ID)
		}
		seen[shot.ID] = true
		if shot.Bars < 1 {
			return nil, fmt.Errorf("%s：镜头 %s 至少占 1 小节，收到 %d", PlanFile, shot.ID, shot.Bars)
		}
		total += shot.Bars
	}
	bars := len(g.Downbeats)
	if total != bars {
		return nil, fmt.Errorf("%s 的小节合计 %d，与节拍网格的 %d 小节不符：每个小节都必须恰好属于一个镜头", PlanFile, total, bars)
	}
	end := TotalFrames(g.Duration, fps)
	segments := make([]Segment, 0, len(p.Scenes))
	firstBar, start := 1, 0
	for i, shot := range p.Scenes {
		next := end
		if i+1 < len(p.Scenes) {
			next = FrameOf(g.Downbeats[firstBar-1+shot.Bars], fps)
		}
		if next <= start {
			return nil, fmt.Errorf("镜头 %s 在 %dfps 下不足一帧：相邻重拍太近，合并进相邻镜头", shot.ID, fps)
		}
		segments = append(segments, Segment{ID: shot.ID, FirstBar: firstBar, Bars: shot.Bars, StartFrame: start, Frames: next - start})
		firstBar += shot.Bars
		start = next
	}
	return segments, nil
}

// CutFrames 返回全部合法切点帧号：每个重拍一帧。切点不在其中即为没卡上点。
func (g Grid) CutFrames(fps int) map[int]bool {
	cuts := make(map[int]bool, len(g.Downbeats))
	for _, d := range g.Downbeats {
		cuts[FrameOf(d, fps)] = true
	}
	return cuts
}
