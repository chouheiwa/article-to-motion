// Package beat 是无配音卡点制作的时间契约：音乐 → 人工确认的节拍网格 →
// 按小节切分的镜头。
//
// 卡点的「切点落在重拍上」不靠渲染后对轴，而由结构保证：镜头时长只能用小节数
// 表达，切点帧号只由一个公式（重拍秒数 × fps 四舍五入）算出。全片只有这一个
// 时间来源，画面与音乐不会因为两处取整方式不同而慢慢错开。
package beat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// Store 是卡点产物目录，相对项目根。
const Store = "production/beat"

// ConfigFile 存在即启用卡点模式。
const ConfigFile = "beat.yaml"

// 项目内的固定文件名。镜头 cue 只认 GridFile，草稿不能直接拿去切镜头。
const (
	TrackFile    = Store + "/track.json"
	DraftFile    = Store + "/grid-draft.json"
	GridFile     = Store + "/grid.json"
	ClickFile    = Store + "/grid-click.wav"
	PlanFile     = Store + "/plan.json"
	AnalysisFile = Store + "/analysis.wav"
)

// DefaultConfig 是 am beat init 写出的 beat.yaml。
const DefaultConfig = `schema: 1
# 节拍模型：final0（约 78 MB，默认）或 small0（约 8 MB，精度略低）；
# 同系列还有 final1/final2、small1/small2，是不同随机种子训练的同结构模型。
checkpoint: final0
# 装有 beat-this==` + AnalyzerVersion + ` 的独立 Python 环境
python: python3
`

// Config 是 beat.yaml 的内容。
type Config struct {
	Schema     int    `yaml:"schema"`
	Checkpoint string `yaml:"checkpoint"`
	Python     string `yaml:"python"`
}

var checkpoints = map[string]bool{
	"final0": true, "final1": true, "final2": true,
	"small0": true, "small1": true, "small2": true,
}

// Enabled 报告项目根是否启用了卡点模式。
func Enabled(root string) bool {
	_, err := os.Stat(filepath.Join(root, ConfigFile))
	return err == nil
}

// LoadConfig 读取并校验 beat.yaml。卡点与歌曲、多角色对话互斥：
// 三者的时间轴各有唯一来源，混用时镜头时长该听谁的没有定义。
func LoadConfig(root string) (Config, error) {
	var c Config
	for _, other := range []string{"song.yaml", "cast.yaml"} {
		if _, err := os.Stat(filepath.Join(root, other)); err == nil {
			return c, fmt.Errorf("卡点模式不能与 %s 同时启用", other)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if err != nil {
		return c, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("%s 字段无效：%w", ConfigFile, err)
	}
	if c.Schema != 1 || !checkpoints[c.Checkpoint] || strings.TrimSpace(c.Python) == "" {
		return c, fmt.Errorf("%s：schema 必须为 1，checkpoint 取 final0..2 或 small0..2，python 必填", ConfigFile)
	}
	return c, nil
}

// localPath 把项目内相对路径解析成绝对路径，拒绝越界与符号链接。
func localPath(root, rel string) (string, error) {
	if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("项目路径必须是安全的相对路径：%q", rel)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := abs
	for _, part := range strings.Split(filepath.Clean(filepath.FromSlash(rel)), string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("卡点产物路径不得包含符号链接：%s", rel)
		}
	}
	return path, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// encodeJSON 是写盘与算摘要共用的唯一编码：两边各编一次的话，缩进或换行
// 稍有不同，摘要就永远对不上。
func encodeJSON(v any) ([]byte, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func writeJSON(path string, v any) error {
	body, err := encodeJSON(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, body, 0o644)
}

// readJSON 严格解码：未知字段与多余内容都报错，手改文件时的笔误不会被静默忽略。
func readJSON(path string, v any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("JSON 文件无效：%s（%w）", path, err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("JSON 文件有多余内容：%s", path)
	}
	return nil
}
