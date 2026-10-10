package scene

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// ToolVersion 是写进输入指纹的 am 版本，由 internal/cli 在启动时设为 cli.Version。
//
// 换了 am 版本，提示词措辞、技能清单和校验规则都可能变了，同一份 scene.json
// 渲出的已不是同一件东西，所以版本号算作一项输入。
var ToolVersion = "dev"

// RecordFile 是镜头目录里的渲染记录文件名。
const RecordFile = ".am-render.json"

// recordSchema 是渲染记录的结构版本。
const recordSchema = 1

// Inputs 是一次渲染的全部输入摘要。
//
// Parts 的键是输入项名字（prompt、renderer、am、file:<相对路径>、skill:<技能名>），
// 值是该项内容的 sha256。分项记下而不是只存总指纹，是为了在判定 Stale 时能说出
// 到底是哪一项变了——只说「输入变了」的话，人还得自己去翻是谁改了什么。
type Inputs struct {
	Fingerprint string            `json:"fingerprint"`
	Parts       map[string]string `json:"parts"`
}

// Record 是镜头产物通过校验时写下的渲染记录。
//
// 它回答的问题是「这份已验收的产物，是不是由当前这套输入渲出来的」，而不是
// 「同样的输入能不能复现它」——渲染由 AI 完成，本来就不可复现。所以指纹一致时
// 跳过重渲染是对的，指纹不一致时也只标 Stale，仍要显式 --force 才会覆盖。
type Record struct {
	Schema     int    `json:"schema"`
	Output     string `json:"output"`
	OutputSize int64  `json:"output_size"`
	OutputSHA  string `json:"output_sha256"`
	Inputs
}

// Fingerprint 计算镜头的输入指纹。renderer 是实际使用的渲染工具，
// resolvedSkills 由 ResolveAllSkills 解析。
//
// 只算声明过的输入，不扫整个镜头目录：渲染器自己会往目录里写合成代码与素材，
// 把它们算进来的话，渲染一结束指纹就变了，记录永远对不上。
func Fingerprint(s Scene, renderer string, resolvedSkills map[string]string) (Inputs, error) {
	// 重试说明与 worker 数是调度层每次临时填的，不是镜头的输入。
	s.RetryNote = ""
	s.RenderWorkers = 0
	prompt, err := BuildPrompt(s, resolvedSkills)
	if err != nil {
		return Inputs{}, err
	}
	parts := map[string]string{
		"renderer": digestString(renderer),
		"am":       digestString(ToolVersion),
	}
	// 技能目录的绝对路径换成技能名：项目整体挪个位置不该让所有镜头变成 Stale，
	// 技能内容的变化由下面的 skill:<名字> 单独记录。
	names := make([]string, 0, len(resolvedSkills))
	for name := range resolvedSkills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dir := resolvedSkills[name]
		if dir == "" {
			continue
		}
		prompt = strings.ReplaceAll(prompt, dir, "<skill:"+name+">")
		digest, err := digestTreeCached(dir)
		if err != nil {
			return Inputs{}, fmt.Errorf("计算技能 %s 的内容摘要：%w", name, err)
		}
		parts["skill:"+name] = digest
	}
	parts["prompt"] = digestString(prompt)
	files := []string{"scene.json", s.Transcript}
	if _, err := os.Stat(filepath.Join(s.Directory, "prompt.md")); err == nil {
		files = append(files, "prompt.md")
	}
	if s.StyleGuide != "" {
		files = append(files, s.StyleGuide)
	}
	files = append(files, timingInputFiles(s)...)
	for _, rel := range files {
		digest, err := digestFile(filepath.Join(s.Directory, rel))
		if err != nil {
			return Inputs{}, err
		}
		parts["file:"+filepath.ToSlash(rel)] = digest
	}
	// 角色包是一棵目录：换了姿势图或 dna.md，画出来的角色就不一样了。
	// song / beat 的数据文件不单独算：它们的 sha256 已写在 scene.json 的 cue 里。
	if s.Cast != nil {
		digest, err := digestTree(filepath.Join(s.Directory, s.Cast.PackDir))
		if err != nil {
			return Inputs{}, fmt.Errorf("计算角色包摘要：%w", err)
		}
		parts["cast:"+filepath.ToSlash(s.Cast.PackDir)] = digest
	}
	return Inputs{Fingerprint: combine(parts), Parts: parts}, nil
}

// Changed 列出与 other 相比内容不同、新增或消失的输入项，按名字排序。
func (in Inputs) Changed(other Inputs) []string {
	seen := make(map[string]bool)
	var changed []string
	for name, digest := range in.Parts {
		seen[name] = true
		if other.Parts[name] != digest {
			changed = append(changed, name)
		}
	}
	for name := range other.Parts {
		if !seen[name] {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func (s Scene) recordPath() string { return filepath.Join(s.Directory, RecordFile) }

// WriteRecord 在产物通过校验后写下渲染记录。
func WriteRecord(s Scene, inputs Inputs) error {
	size, digest, err := outputIdentity(s)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(Record{Schema: recordSchema, Output: s.Output, OutputSize: size, OutputSHA: digest, Inputs: inputs}, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.recordPath(), append(body, '\n'), 0o644)
}

// LoadRecord 读出仍然适用于当前产物的渲染记录。
//
// 产物被替换过（大小或内容摘要对不上）时记录不再适用，返回 ok=false，
// 由调用方退回旧的修改时间判定——那份记录描述的是另一个文件。
func LoadRecord(s Scene) (Record, bool) {
	body, err := os.ReadFile(s.recordPath())
	if err != nil {
		return Record{}, false
	}
	var record Record
	if json.Unmarshal(body, &record) != nil || record.Schema != recordSchema || record.Output != s.Output || record.Fingerprint == "" {
		return Record{}, false
	}
	size, digest, err := outputIdentity(s)
	if err != nil || size != record.OutputSize || digest != record.OutputSHA {
		return Record{}, false
	}
	return record, true
}

// RemoveRecord 删除渲染记录。开始重渲染前调用：渲染中途失败时目录里可能
// 留下一个恰好通过校验的半成品，旧记录不能替它作保。
func RemoveRecord(s Scene) error {
	err := os.Remove(s.recordPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func outputIdentity(s Scene) (int64, string, error) {
	path := s.OutputPath()
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", err
	}
	digest, err := digestFile(path)
	if err != nil {
		return 0, "", err
	}
	return info.Size(), digest, nil
}

func combine(parts map[string]string) string {
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s=%s\n", name, parts[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// digestTree 对目录下全部普通文件的相对路径与内容做摘要，顺序固定。
// 符号链接按链接目标的路径文本计入，不跟进去：技能树与角色包里都不该有它们。
func digestTree(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "link %s -> %s\n", rel, target)
		case entry.Type().IsRegular():
			digest, err := digestFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "file %s %s\n", rel, digest)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// treeDigests 缓存技能目录摘要：run-all 开跑前要给每个镜头算一遍指纹，
// 几十个镜头共用同一套技能，没必要把同一棵树读几十遍。进程只活一条命令，
// 期间技能树不会变。
var treeDigests sync.Map

func digestTreeCached(root string) (string, error) {
	if cached, ok := treeDigests.Load(root); ok {
		return cached.(string), nil
	}
	digest, err := digestTree(root)
	if err != nil {
		return "", err
	}
	treeDigests.Store(root, digest)
	return digest, nil
}
