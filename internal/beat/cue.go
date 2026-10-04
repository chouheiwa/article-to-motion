package beat

import (
	"fmt"
	"math"
	"path/filepath"
)

// CueDataFile 是写进每个镜头目录的局部节拍文件名。
const CueDataFile = "beat-cues.json"

// Cue 是 scene.json 的 beat 块：把镜头绑定到全局网格的某一段。
// 路径相对镜头目录。
type Cue struct {
	Scene      string `json:"scene"`
	FirstBar   int    `json:"first_bar"`
	Bars       int    `json:"bars"`
	FPS        int    `json:"fps"`
	Grid       string `json:"grid"`
	SHA        string `json:"sha256"`
	StartFrame int    `json:"start_frame"`
	Frames     int    `json:"frames"`
	Data       string `json:"data"`
	DataSHA    string `json:"data_sha256"`
}

// Local 是镜头局部的节拍表。帧号已减去镜头起点，渲染器直接按它编排。
type Local struct {
	FPS      int        `json:"fps"`
	Frames   int        `json:"frames"`
	FirstBar int        `json:"first_bar"`
	Bars     int        `json:"bars"`
	Beats    []LocalHit `json:"beats"`
}

// LocalHit 是镜头内的一拍。Bar 是全曲小节编号（0 表示第一个重拍之前的弱起），
// Beat 是这一拍在小节内的序号，从 1 开始；Beat == 1 即重拍。
type LocalHit struct {
	Frame    int     `json:"frame"`
	Seconds  float64 `json:"seconds"`
	Bar      int     `json:"bar"`
	Beat     int     `json:"beat"`
	Downbeat bool    `json:"downbeat"`
}

// Slice 取出 [StartFrame, StartFrame+Frames) 内的拍点。
func (g Grid) Slice(segment Segment, fps int) Local {
	local := Local{FPS: fps, Frames: segment.Frames, FirstBar: segment.FirstBar, Bars: segment.Bars, Beats: []LocalHit{}}
	downIndex := make(map[int]bool, len(g.Downbeats))
	for _, d := range g.Downbeats {
		if i, ok := nearestBeat(g.Beats, d); ok {
			downIndex[i] = true
		}
	}
	bar, inBar := 0, 0
	for i, b := range g.Beats {
		isDown := downIndex[i]
		if isDown {
			bar++
			inBar = 0
		}
		inBar++
		frame := FrameOf(b, fps) - segment.StartFrame
		if frame < 0 || frame >= segment.Frames {
			continue
		}
		local.Beats = append(local.Beats, LocalHit{
			Frame: frame, Seconds: float64(frame) / float64(fps),
			Bar: bar, Beat: inBar, Downbeat: isDown,
		})
	}
	return local
}

// EncodeLocal 返回局部节拍文件的字节与摘要。写盘与 Cue.DataSHA 用同一份字节。
func EncodeLocal(local Local) ([]byte, string, error) {
	body, err := encodeJSON(local)
	if err != nil {
		return nil, "", err
	}
	return body, hashBytes(body), nil
}

// GridSHA 返回正式网格文件的摘要。
func GridSHA(root string) (string, error) {
	path, err := localPath(root, GridFile)
	if err != nil {
		return "", err
	}
	return hashFile(path)
}

// VerifyCue 核对镜头的 beat 块仍与全局网格、分镜计划一致，且切点卡在重拍上。
//
// 切点是精确比对，不留容差：帧号只由 FrameOf 一个公式算出，任何偏差都说明
// 有人手改了镜头时长，而不是取整误差。成片里的 ±1 帧容差属于整片复查。
//
// 项目根从镜头目录向上找 beat.yaml 确定，不从 cue.Grid 反推：反推的话，
// 指向别的项目的 "../../other/production/beat/grid.json" 也能自洽通过。
// cue 还带着镜头 id 与小节区间，复制别的镜头的 beat 块过来会被拦下。
func VerifyCue(sceneDir, sceneID string, cue Cue, durationSeconds float64) error {
	root, err := FindRoot(sceneDir)
	if err != nil {
		return err
	}
	gridPath := filepath.Join(root, filepath.FromSlash(GridFile))
	if want, err := GridRef(sceneDir, root); err != nil || cue.Grid != want {
		return fmt.Errorf("beat.grid 必须是指向本项目 %s 的相对路径 %q", GridFile, want)
	}
	if cue.Scene != sceneID {
		return fmt.Errorf("beat 块属于镜头 %q，不是 %q：请重新运行 am beat cues", cue.Scene, sceneID)
	}
	grid, err := LoadGrid(root)
	if err != nil {
		return err
	}
	sum, err := hashFile(gridPath)
	if err != nil {
		return err
	}
	if sum != cue.SHA {
		return fmt.Errorf("节拍网格已变化，请重新运行 am beat cues 并重渲染")
	}
	if cue.FPS <= 0 || cue.Frames <= 0 || cue.StartFrame < 0 {
		return fmt.Errorf("beat 块的帧率或帧区间无效")
	}
	if math.Abs(durationSeconds*float64(cue.FPS)-float64(cue.Frames)) > 1e-6 {
		return fmt.Errorf("duration_seconds 与 beat 块的 %d 帧不符：镜头时长由 am beat cues 写入，不要手改", cue.Frames)
	}
	cuts := grid.CutFrames(cue.FPS)
	end := cue.StartFrame + cue.Frames
	if cue.StartFrame != 0 && !cuts[cue.StartFrame] {
		return fmt.Errorf("镜头起点第 %d 帧不在任何重拍上", cue.StartFrame)
	}
	if end != TotalFrames(grid.Duration, cue.FPS) && !cuts[end] {
		return fmt.Errorf("镜头终点第 %d 帧不在任何重拍上，也不是音乐结尾", end)
	}
	segment, err := segmentOf(root, grid, cue)
	if err != nil {
		return err
	}
	_, want, err := EncodeLocal(grid.Slice(segment, cue.FPS))
	if err != nil {
		return err
	}
	dataPath, err := localPath(sceneDir, cue.Data)
	if err != nil {
		return err
	}
	have, err := hashFile(dataPath)
	if err != nil {
		return fmt.Errorf("找不到局部节拍文件：%w", err)
	}
	if have != cue.DataSHA || have != want {
		return fmt.Errorf("%s 与全局网格不一致，请重新运行 am beat cues", cue.Data)
	}
	return nil
}

// segmentOf 从分镜计划里找回这个 cue 对应的镜头区间。
func segmentOf(root string, grid Grid, cue Cue) (Segment, error) {
	plan, err := LoadPlan(root)
	if err != nil {
		return Segment{}, err
	}
	segments, err := Layout(grid, plan, cue.FPS)
	if err != nil {
		return Segment{}, err
	}
	for _, s := range segments {
		if s.ID == cue.Scene {
			if s.StartFrame != cue.StartFrame || s.Frames != cue.Frames || s.FirstBar != cue.FirstBar || s.Bars != cue.Bars {
				return Segment{}, fmt.Errorf("镜头 %s 的区间与 %s 不符：计划第 %d 小节起 %d 小节（%d+%d 帧），beat 块第 %d 小节起 %d 小节（%d+%d 帧）",
					s.ID, PlanFile, s.FirstBar, s.Bars, s.StartFrame, s.Frames, cue.FirstBar, cue.Bars, cue.StartFrame, cue.Frames)
			}
			return s, nil
		}
	}
	return Segment{}, fmt.Errorf("镜头 %s 不在 %s 里，请重新运行 am beat cues", cue.Scene, PlanFile)
}

// FindRoot 从 dir 向上找到含 beat.yaml 的项目根。
func FindRoot(dir string) (string, error) {
	current, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if Enabled(current) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("从 %s 向上找不到 %s", dir, ConfigFile)
		}
		current = parent
	}
}

// GridRef 是镜头目录引用正式网格时唯一合法的写法。
func GridRef(sceneDir, root string) (string, error) {
	absDir, err := filepath.Abs(sceneDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absDir, filepath.Join(root, filepath.FromSlash(GridFile)))
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
