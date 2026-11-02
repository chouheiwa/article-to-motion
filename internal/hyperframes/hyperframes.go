// Package hyperframes 把上游官方技能按固定 commit 装进用户项目。
//
// 不调用上游的 `hyperframes skills`，有两个各自独立的理由。
//
// 一、它只认 homedir()：把技能写进 ~/.claude/skills 与 ~/.agents/skills，再
// 镜像到本机其余几十个 agent 目录。没有任何命令行选项或环境变量能改这个落点
// （`skills update --dir` 的帮助原文就写着 "scopes the prune, not the install"）。
// 装在机器级有三个后果，第三个是硬伤：
//
//   - 项目不自包含。整个项目拷到另一台机器就渲不出来，技能不在里面。
//   - 污染用户 HOME，一次安装会写进本机所有已安装 agent 的目录。
//   - 项目之间互相覆盖。下发的 PROMPT 要求「固定 HyperFrames 版本为 X」，
//     可机器上只有一份技能：两个项目固定不同版本时谁后初始化谁说了算，
//     另一个项目的固定版本悄悄失效。
//
// 二、它给不了可复现性。它 git clone 仓库的**默认分支**再取技能，没有任何
// 指定 tag 或 commit 的口子。实测同机分别用 hyperframes@0.8.1 与 @0.8.14
// 安装，产出的 26 个技能逐字节相同、都等于当天 main 的状态——npm 版本号固定
// 的只是 CLI 二进制，而技能内容直接决定成片动效。
//
// 所以这里自己按 tag v<Version> 浅克隆上游仓库，从 skills/ 与 .agents/skills/
// 两处取出含 SKILL.md 的目录搬进项目的 .agents/skills/。实测这套筛选在同一个
// commit 上与上游安装器的产出逐字节相同。确定性由 cloneSkills 的三个环境开关
// 与 PinnedSkillsCommit / PinnedSkillsCount 两道校验保证。
//
// scene.ResolveSkill 本来就优先项目级候选，搬进去即可被优先命中，不需要改
// 解析逻辑。安装只依赖 git，不再依赖 Node/npx。
package hyperframes

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
)

// SkillsSubdir 是技能在项目里的落点，与 scene.portableSkillsDir 一致。
const SkillsSubdir = ".agents/skills"

// PinnedVersion 是 am init 安装的 HyperFrames 上游版本。
//
// 固定版本不自动前进是刻意的：技能内容变化会直接改变成片动效。升级前要对比
// 两版 skills/ 树与自动内联字体清单（CANONICAL_FONTS），确认 internal/scene
// 注册的技能与提示词点名的参考文件都还在、字体清单没变，并同步
// internal/validate 的 autoEmbeddedFonts 注释。
//
// 0.8.14 → 0.8.225 是一次结构性升级：0.8.185 起上游把 hyperframes-animation、
// hyperframes-core、hyperframes-creative 等技能并进入口技能 hyperframes，原子
// rule 索引随之消失，internal/scene 的注册与提示词片段因此重写过一遍。
//
// ⚠️ 这个版本号固定的是 CLI 二进制，不是技能内容：上游 `hyperframes skills`
// 直接 git clone 仓库默认分支取 skills/，安装器没有任何指定 ref 的口子。
// 实测 0.8.1 与 0.8.14 装出来的 26 个技能逐字节相同，且都等于当天 main 的
// 状态——所以两次 am init 之间技能可能已经变了，与这里的版本号无关。
// 详见 README「HyperFrames 版本固定的边界」。
//
// 这里是唯一来源：命令行、错误信息、--help 与下发提示词里的锁定指令都从它
// 取值（提示词经 internal/preset/gen 的 {{HYPERFRAMES_VERSION}} 占位符注入，
// 由根目录 assets_test.go 的 TestPresetsPinSameHyperFramesVersionAsBinary 守住）。
const PinnedVersion = "0.8.225"

// SkillsRepoURL 是上游技能的来源仓库。
const SkillsRepoURL = "https://github.com/heygen-com/hyperframes.git"

// PinnedSkillsCommit 是 v<PinnedVersion> 这个 tag 指向的 commit。
//
// 只钉 tag 不够：tag 可以被 force-push 移动，移动之后按 tag 克隆会静默拿到
// 另一份技能而版本号一个字没变。解析出的 commit 与这里对不上就拒装。
const PinnedSkillsCommit = "b687d062353f2dd43a9a4b8b77aafeb1077b3875"

// PinnedSkillsCount 是该 commit 上应当拼装出的技能数。
//
// 技能来自 skills/ 与 .agents/skills/ 两处含 SKILL.md 的目录。上游哪天挪动
// 目录，这套拼装会静默少装一批——而渲染 agent 找不到技能不会报错，只会自己
// 发明一套多半不挂在 paused timeline 上的写法。数量对不上就拒装，顺带让
// 「升版本时顺手核对一遍」这件事自己会响。
const PinnedSkillsCount = 14

// skillsSubdirsInRepo 是上游仓库里存放技能的两个位置。
//
// 实测依据：0.8.14 时按这两处筛出含 SKILL.md 的目录（20 + 6），与上游安装器
// `hyperframes skills` 在同一个 commit 上的产出逐字节相同；0.8.225 上是 8 + 6。
var skillsSubdirsInRepo = []string{"skills", filepath.ToSlash(SkillsSubdir)}

// manifestFile 是判定一个目录是不是技能的依据，与 scene 包一致。
const manifestFile = "SKILL.md"

// Options 描述一次安装。
type Options struct {
	// ProjectDir 是用户项目根目录。
	ProjectDir string
	// Version 是要安装的上游版本，例如 "0.8.225"。技能按 tag v<Version> 克隆。
	Version string
	// RepoURL 是技能来源仓库，留空取 SkillsRepoURL。测试用它指向本地夹具仓库。
	RepoURL string
	// ExpectCommit 是 tag 必须解析到的 commit，留空取 PinnedSkillsCommit。
	// 显式传入 "-" 表示跳过校验（只在无法预知 commit 的夹具场景下使用）。
	ExpectCommit string
	// ExpectSkills 是期望拼装出的技能数，留空（0）取 PinnedSkillsCount。
	ExpectSkills int
	// Env 是当前进程环境。安装器要在改写过 HOME 的副本里运行，
	// 但 PATH、代理、npm 配置这些必须沿用，否则公司内网装不上。
	Env map[string]string
	// Protected 列出不允许被上游同名技能覆盖的技能，即随二进制下发的那些。
	Protected map[string]bool
	// Output 接收安装器的进度输出。
	Output io.Writer
}

// Result 汇报安装结果。
type Result struct {
	// Installed 是写进项目的技能名，已排序。
	Installed []string
	// Protected 是因为与内置技能重名而跳过的上游技能名。
	Protected []string
}

// Install 把固定版本的上游技能装进 opts.ProjectDir 下的 .agents/skills/。
//
// 全程不碰用户 HOME：安装器在一个临时目录里以为自己在写 home，结束后整棵树
// 被搬进项目，临时目录删除。
func Install(ctx context.Context, opts Options) (Result, error) {
	if opts.Version == "" {
		return Result{}, fmt.Errorf("必须指定 HyperFrames 版本")
	}
	if opts.RepoURL == "" {
		opts.RepoURL = SkillsRepoURL
	}
	if opts.ExpectCommit == "" {
		opts.ExpectCommit = PinnedSkillsCommit
	}
	if opts.ExpectSkills == 0 {
		opts.ExpectSkills = PinnedSkillsCount
	}
	git, err := envutil.LookPath("git", opts.Env["PATH"])
	if err != nil {
		return Result{}, fmt.Errorf("找不到 git，无法获取 HyperFrames 技能：%w", err)
	}
	// 克隆目录必须在项目之外：放项目里会把 .git 和中间产物留在交付目录，
	// 而且 am archive 会连它们一起归档。
	tmp, err := os.MkdirTemp("", "am-hyperframes-")
	if err != nil {
		return Result{}, fmt.Errorf("无法创建临时目录：%w", err)
	}
	defer os.RemoveAll(tmp)

	commit, err := cloneSkills(ctx, git, tmp, opts)
	if err != nil {
		return Result{}, err
	}

	sources := make([]string, 0, len(skillsSubdirsInRepo))
	for _, sub := range skillsSubdirsInRepo {
		path := filepath.Join(tmp, filepath.FromSlash(sub))
		if _, statErr := os.Stat(path); statErr == nil {
			sources = append(sources, path)
		}
	}
	if len(sources) == 0 {
		return Result{}, fmt.Errorf("上游仓库 %s 在 %s 上没有 %v 中的任何一处，技能目录结构已变",
			opts.RepoURL, commit, skillsSubdirsInRepo)
	}
	result, err := copySkills(sources, filepath.Join(opts.ProjectDir, SkillsSubdir), opts.Protected)
	if err != nil {
		return Result{}, err
	}
	if total := len(result.Installed) + len(result.Protected); total != opts.ExpectSkills {
		return Result{}, fmt.Errorf(
			"上游 %s 拼装出 %d 个技能，期望 %d 个：技能目录结构可能已变，"+
				"核对 %v 两处后同步 PinnedSkillsCount",
			commit, total, opts.ExpectSkills, skillsSubdirsInRepo)
	}
	// 记录这次到底装到了哪一份技能：commit 加每个技能目录的内容摘要。
	// 版本号本身不足以还原——上游安装器不认 ref，这份记录才是可复现的凭据。
	if err := WriteManifest(opts.ProjectDir, opts.Version, commit, result.Installed); err != nil {
		return Result{}, err
	}
	return result, nil
}

// cloneSkills 按 tag v<Version> 浅克隆上游仓库，只检出技能所在的两个目录，
// 校验解析出的 commit，返回该 commit。
//
// 三个开关都是承重的：
//
//   - GIT_LFS_SKIP_SMUDGE=1：仓库里有 LFS 管理的媒体文件。装了 git-lfs 的机器
//     会把它们还原成真文件，没装的机器留下 132 字节的指针——同一个 commit 在
//     两台机器上得到不同的树，"可复现"当场失效。上游安装器本身也不拉 LFS，
//     所以跳过 smudge 同时也是与它产出保持逐字节一致的前提。
//   - --filter=blob:none --sparse：仓库带着 packages/ 和测试产物，完整克隆几百 MB。
//   - GIT_CONFIG_GLOBAL/SYSTEM=/dev/null：用户的 git 配置（钩子、filter、
//     autocrlf）不能参与，否则同一个 commit 在不同人机器上检出结果不同。
func cloneSkills(ctx context.Context, git, dir string, opts Options) (string, error) {
	env := envutil.EnvList(cloneEnv(opts.Env))
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stderr = opts.Output
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	tag := "v" + opts.Version
	// -c advice.detachedHead=false：按 tag 克隆必然是 detached HEAD，git 会
	// 往 stderr 打一段给人看的忠告，而 stderr 是接到 am 输出上的。
	if _, err := run("-c", "advice.detachedHead=false", "clone", "--quiet",
		"--depth", "1", "--filter=blob:none", "--sparse", "--branch", tag,
		opts.RepoURL, "."); err != nil {
		return "", fmt.Errorf("克隆 %s 的 %s 失败：%w", opts.RepoURL, tag, err)
	}
	if _, err := run(append([]string{"sparse-checkout", "set"}, skillsSubdirsInRepo...)...); err != nil {
		return "", fmt.Errorf("检出技能目录失败：%w", err)
	}
	commit, err := run("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("无法解析 %s 指向的 commit：%w", tag, err)
	}
	if opts.ExpectCommit != "-" && commit != opts.ExpectCommit {
		return "", fmt.Errorf(
			"%s 指向 %s，与固定的 %s 不符：tag 被移动过，拒绝安装",
			tag, commit, opts.ExpectCommit)
	}
	return commit, nil
}

// cloneEnv 组装 git 的运行环境：沿用 PATH 与代理设置，但屏蔽用户的 git 配置。
func cloneEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env)+3)
	for key, value := range env {
		out[key] = value
	}
	out["GIT_LFS_SKIP_SMUDGE"] = "1"
	out["GIT_CONFIG_GLOBAL"] = os.DevNull
	out["GIT_CONFIG_SYSTEM"] = os.DevNull
	out["GIT_TERMINAL_PROMPT"] = "0"
	return out
}

// copySkills 把 sources 各目录下的每个技能目录整体搬进 dest。
//
// 收多个来源是因为上游把技能放在仓库的两处（skills/ 与 .agents/skills/）。
// 判定标准仍是"目录里有 SKILL.md"，与上游安装器的筛选结果实测逐字节一致。
//
// 上游技能由 am 按固定 commit 管理，所以同名目录整体替换而不是合并：残留上
// 一版的文件会让技能树处于两个版本混合的状态，而那正是固定版本要消除的东西。
// 随二进制下发的技能受 protected 保护，永远不会被上游同名技能覆盖。
func copySkills(sources []string, dest string, protected map[string]bool) (Result, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Result{}, err
	}
	var result Result
	seen := make(map[string]string)
	for _, source := range sources {
		entries, err := os.ReadDir(source)
		if err != nil {
			return Result{}, fmt.Errorf("无法读取安装产物：%w", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(source, name, manifestFile)); statErr != nil {
				continue // 不是技能目录
			}
			// 两处出现同名技能时哪一份生效取决于遍历顺序，是静默的不确定性。
			if previous, dup := seen[name]; dup {
				return Result{}, fmt.Errorf("技能 %s 在 %s 与 %s 两处重复，无法确定用哪一份",
					name, previous, source)
			}
			seen[name] = source
			if protected[name] {
				result.Protected = append(result.Protected, name)
				continue
			}
			target := filepath.Join(dest, name)
			if err := os.RemoveAll(target); err != nil {
				return Result{}, err
			}
			if err := copyTree(filepath.Join(source, name), target); err != nil {
				return Result{}, fmt.Errorf("拷贝技能 %s 失败：%w", name, err)
			}
			result.Installed = append(result.Installed, name)
		}
	}
	if len(result.Installed) == 0 {
		return Result{}, fmt.Errorf("安装产物里没有任何技能目录：%v", sources)
	}
	sort.Strings(result.Installed)
	sort.Strings(result.Protected)
	return result, nil
}

// copyTree 递归拷贝目录。符号链接一律拒绝：上游在 Unix 上用符号链接做的是
// 「镜像到其他 agent 目录」，指回它自己的 home store；搬进项目后那些链接会指向
// 一个已经删掉的临时目录，成为静默失效的空技能。
func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relative)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("安装产物含符号链接，无法安全搬进项目：%s", relative)
		case !entry.Type().IsRegular():
			return nil // 设备、套接字之类一律忽略
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, info.Mode().Perm())
	})
}
