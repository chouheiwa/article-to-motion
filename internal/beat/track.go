package beat

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

// Track 是导入项目的那首音乐。网格、镜头 cue 都绑定它的摘要。
type Track struct {
	Audio    string  `json:"audio"`
	AudioSHA string  `json:"audio_sha256"`
	Duration float64 `json:"duration_seconds"`
	Source   string  `json:"source"`
}

// MediaChecker 是导入时用到的媒体操作；mediaprobe.Toolchain 满足它。
type MediaChecker interface {
	Probe(path string) (mediaprobe.Media, error)
	Decode(path string) error
}

var audioExtensions = map[string]bool{".wav": true, ".mp3": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".opus": true}

// Import 把音乐复制进项目并记录摘要与实测时长。
//
// 已导入另一首音乐时默认拒绝：换歌会让已确认的网格和所有镜头 cue 失效，
// 这种事要用户明确说（replace）。同一首重复导入是幂等的。
func Import(root, source string, media MediaChecker, replace bool) (Track, error) {
	if _, err := LoadConfig(root); err != nil {
		return Track{}, err
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return Track{}, fmt.Errorf("找不到音乐文件：%s", source)
	}
	ext := strings.ToLower(filepath.Ext(source))
	if !audioExtensions[ext] {
		return Track{}, fmt.Errorf("不支持的音乐格式 %q：可用 wav、mp3、m4a、aac、flac、ogg、opus", ext)
	}
	probed, err := media.Probe(source)
	if err != nil {
		return Track{}, err
	}
	if probed.Audio == nil || probed.Video != nil {
		return Track{}, fmt.Errorf("音乐文件必须只有音轨：%s", source)
	}
	if probed.DurationSeconds <= 0 || math.IsNaN(probed.DurationSeconds) || math.IsInf(probed.DurationSeconds, 0) {
		return Track{}, fmt.Errorf("无法确定音乐时长：%s", source)
	}
	if err := media.Decode(source); err != nil {
		return Track{}, fmt.Errorf("音乐无法完整解码：%w", err)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return Track{}, err
	}
	sum := hashBytes(body)
	// 文件名带内容摘要：替换时新旧音乐不会同名，可以先写新文件、再改记录、
	// 最后删旧文件，任何一步失败都不会让 track.json 指向一个已被删掉或覆盖的文件。
	track := Track{Audio: "music-" + sum[:12] + ext, AudioSHA: sum, Duration: probed.DurationSeconds, Source: filepath.Base(source)}
	var stale string
	if existing, err := LoadTrack(root); err == nil {
		if existing.AudioSHA == track.AudioSHA {
			return existing, nil
		}
		if !replace {
			return Track{}, fmt.Errorf("项目已导入 %s；换歌会让已确认的节拍网格与镜头 cue 全部失效，确认后加 --replace", existing.Source)
		}
		if existing.Audio != track.Audio {
			stale = existing.Audio
		}
	} else if !errors.Is(err, fs.ErrNotExist) && !replace {
		return Track{}, fmt.Errorf("现有 %s 不可用（%v）；确认要覆盖时加 --replace", TrackFile, err)
	}
	dest, err := localPath(root, Store+"/"+track.Audio)
	if err != nil {
		return Track{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Track{}, err
	}
	if err := fsutil.AtomicWrite(dest, body, 0o644); err != nil {
		return Track{}, err
	}
	record, err := localPath(root, TrackFile)
	if err != nil {
		return Track{}, err
	}
	if err := writeJSON(record, track); err != nil {
		return Track{}, err
	}
	if stale != "" {
		if old, err := localPath(root, Store+"/"+stale); err == nil {
			_ = os.Remove(old)
		}
	}
	return track, nil
}

// LoadTrack 读取导入记录并核对音乐文件没有被替换。
// 记录不存在时返回的错误满足 errors.Is(err, fs.ErrNotExist)。
func LoadTrack(root string) (Track, error) {
	var track Track
	record, err := localPath(root, TrackFile)
	if err != nil {
		return track, err
	}
	if _, err := os.Stat(record); err != nil {
		return track, err
	}
	if err := readJSON(record, &track); err != nil {
		return track, err
	}
	if !filepath.IsLocal(track.Audio) || strings.ContainsRune(track.Audio, '/') {
		return track, fmt.Errorf("%s 的 audio 必须是 %s 下的文件名", TrackFile, Store)
	}
	audio, err := localPath(root, Store+"/"+track.Audio)
	if err != nil {
		return track, err
	}
	sum, err := hashFile(audio)
	if err != nil {
		return track, fmt.Errorf("找不到已导入的音乐：%w", err)
	}
	if sum != track.AudioSHA {
		return track, fmt.Errorf("音乐文件已被改动，与 %s 记录的摘要不符；请重新 am beat import", TrackFile)
	}
	return track, nil
}

// AudioPath 返回已导入音乐的绝对路径。
func (t Track) AudioPath(root string) (string, error) {
	return localPath(root, Store+"/"+t.Audio)
}
