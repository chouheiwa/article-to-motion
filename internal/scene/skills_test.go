package scene

import (
	"github.com/chouheiwa/articale-to-motion/internal/hyperframes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installSkill 在 dir 下伪造一份完整的 hyperframes-animation 技能安装。
func installSkill(t *testing.T, dir string) string {
	t.Helper()
	skill := filepath.Join(dir, AnimationSkillName)
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", rulesIndexFile} {
		if err := os.WriteFile(filepath.Join(skill, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// resolveAnimationSkill 是这组测试的入口。
//
// 测的是 ResolveSkill 的目录解析逻辑，用动效技能作为样本——它是唯一同时要求
// SKILL.md 和 rules-index.md 两个文件的技能，覆盖面最广。
func resolveAnimationSkill(renderer, sceneDir string, environ map[string]string) (string, error) {
	for _, desc := range RegisteredSkills {
		if desc.Name == AnimationSkillName {
			return ResolveSkill(renderer, sceneDir, environ, desc)
		}
	}
	panic("动效技能不在注册表里")
}

func TestResolveAnimationSkillPrefersExplicitEnvironment(t *testing.T) {
	home := t.TempDir()
	installSkill(t, filepath.Join(home, ".claude", "skills"))
	explicit := installSkill(t, filepath.Join(t.TempDir(), "custom"))

	got, err := resolveAnimationSkill("claude", t.TempDir(), map[string]string{"HOME": home, SkillsDirEnv: explicit})
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Fatalf("want %s, got %s", explicit, got)
	}
}

func TestResolveAnimationSkillRejectsInvalidExplicitEnvironment(t *testing.T) {
	home := t.TempDir()
	installSkill(t, filepath.Join(home, ".claude", "skills"))

	// 显式配置错了必须报错，而不是悄悄回退到家目录里那份能用的安装。
	_, err := resolveAnimationSkill("claude", t.TempDir(), map[string]string{"HOME": home, SkillsDirEnv: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error for an explicitly configured but invalid skills directory")
	}
	if !strings.Contains(err.Error(), SkillsDirEnv) {
		t.Fatalf("error should name the offending variable, got %v", err)
	}
}

func TestResolveAnimationSkillFindsProjectScopeByWalkingUp(t *testing.T) {
	root := t.TempDir()
	project := installSkill(t, filepath.Join(root, ".claude", "skills"))
	sceneDir := filepath.Join(root, "episodes", "episode-03", "scenes", "scene-002")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveAnimationSkill("claude", sceneDir, map[string]string{"HOME": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got != project {
		t.Fatalf("want %s, got %s", project, got)
	}
}

func TestResolveAnimationSkillProjectScopeBeatsHomeScope(t *testing.T) {
	home := t.TempDir()
	installSkill(t, filepath.Join(home, ".claude", "skills"))
	root := t.TempDir()
	project := installSkill(t, filepath.Join(root, ".claude", "skills"))

	got, err := resolveAnimationSkill("claude", root, map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if got != project {
		t.Fatalf("project scope must win: want %s, got %s", project, got)
	}
}

func TestResolveAnimationSkillPerRendererHomeLocation(t *testing.T) {
	cases := map[string]string{
		"claude":    ".claude/skills",
		"codex":     ".codex/skills",
		"qoder":     ".qoder/skills",
		"codebuddy": ".codebuddy/skills",
		"opencode":  ".config/opencode/skills",
	}
	for renderer, relative := range cases {
		t.Run(renderer, func(t *testing.T) {
			home := t.TempDir()
			want := installSkill(t, filepath.Join(home, filepath.FromSlash(relative)))
			// 另一个渲染器的目录也存在，确保按 renderer 而不是按存在性挑。
			installSkill(t, filepath.Join(home, ".cursor", "skills"))

			got, err := resolveAnimationSkill(renderer, t.TempDir(), map[string]string{"HOME": home})
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("want %s, got %s", want, got)
			}
		})
	}
}

func TestResolveAnimationSkillFallsBackToPortableLocation(t *testing.T) {
	home := t.TempDir()
	want := installSkill(t, filepath.Join(home, ".agents", "skills"))

	got, err := resolveAnimationSkill("codex", t.TempDir(), map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestResolveAnimationSkillPrefersRendererDirWhenProjectSitsInsideHome(t *testing.T) {
	// 本项目就长这样：项目根在 $HOME 之下，且家目录同时装了通用的 .agents/skills。
	// 向上遍历必须不能在 $HOME 那一层先命中通用目录。
	home := t.TempDir()
	installSkill(t, filepath.Join(home, ".agents", "skills"))
	want := installSkill(t, filepath.Join(home, ".config", "opencode", "skills"))
	sceneDir := filepath.Join(home, "work", "video", "episodes", "scene-002")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveAnimationSkill("opencode", sceneDir, map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("renderer-specific dir must win over the portable one: want %s, got %s", want, got)
	}
}

func TestResolveAnimationSkillIgnoresIncompleteInstall(t *testing.T) {
	home := t.TempDir()
	// 只有 SKILL.md 没有 rules-index.md：提示词引用的文件不存在，必须当作未找到。
	partial := filepath.Join(home, ".claude", "skills", AnimationSkillName)
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(partial, "SKILL.md"), []byte("x"), 0o644)

	got, err := resolveAnimationSkill("claude", t.TempDir(), map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("want empty, got %s", got)
	}
}

func TestResolveAnimationSkillReturnsEmptyWhenAbsent(t *testing.T) {
	got, err := resolveAnimationSkill("claude", t.TempDir(), map[string]string{"HOME": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("want empty, got %s", got)
	}
}

func TestResolveAnimationSkillUnknownRendererStillUsesPortableLocation(t *testing.T) {
	home := t.TempDir()
	want := installSkill(t, filepath.Join(home, ".agents", "skills"))

	got, err := resolveAnimationSkill("some-future-tool", t.TempDir(), map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestSkillsEnvironmentPrefersProcessEnvOverDotenv(t *testing.T) {
	base := map[string]string{"HOME": "/home/a", SkillsDirEnv: "/from/shell"}
	overlay := map[string]string{SkillsDirEnv: "/from/dotenv"}

	if got := SkillsEnvironment(base, overlay)[SkillsDirEnv]; got != "/from/shell" {
		t.Fatalf("process env must win, got %s", got)
	}
}

func TestSkillsEnvironmentFallsBackToDotenv(t *testing.T) {
	base := map[string]string{"HOME": "/home/a"}
	overlay := map[string]string{SkillsDirEnv: "/from/dotenv"}

	merged := SkillsEnvironment(base, overlay)
	if got := merged[SkillsDirEnv]; got != "/from/dotenv" {
		t.Fatalf("want /from/dotenv, got %s", got)
	}
	if _, polluted := base[SkillsDirEnv]; polluted {
		t.Fatal("SkillsEnvironment must not mutate its input")
	}
}

func TestBuildPromptEmbedsResolvedSkillPath(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	skills := installSkill(t, filepath.Join(t.TempDir(), "skills"))

	prompt, err := BuildPrompt(s, map[string]string{AnimationSkillName: skills})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(skills, AnimationSkillName),
		rulesIndexFile,
		blueprintsIndexFile,
		"动效要求（强制）",
		"decorative",
		// 数量配额会让渲染器为了达标堆砌与文案无关的动效，改为要求每个动效说得出理由。
		"说得出",
		// 实测出现过最后 3 秒画面完全冻结，整画面冻结的上限必须保留。
		"完全冻结超过 3 秒",
		// 与 frame.md 的 ambient_per_scene_max 一致，不得反过来强制贯穿全镜的环境动效。
		"最多 1 条",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPromptFallsBackToSkillNameWithoutPath(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, AnimationSkillName) || !strings.Contains(prompt, rulesIndexFile) {
		t.Fatalf("fallback prompt must still name the skill and its index: %s", prompt)
	}
	if strings.Contains(prompt, "本机该技能目录为") {
		t.Fatal("fallback prompt must not claim a machine-local path")
	}
}

// installNamedSkill 在 dir 下伪造一份任意技能的最小完整安装。
func installNamedSkill(t *testing.T, dir string, desc SkillDescriptor) string {
	t.Helper()
	skill := filepath.Join(dir, desc.Name)
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range desc.RequiredFiles {
		if err := os.WriteFile(filepath.Join(skill, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestEveryRegisteredSkillContributesAPromptSection 保证注册表里加了技能却忘了
// 写提示词片段时会失败——注册但不注入等于白装。
func TestEveryRegisteredSkillContributesAPromptSection(t *testing.T) {
	for _, desc := range RegisteredSkills {
		if desc.PromptSection == nil {
			t.Errorf("%s 没有 PromptSection", desc.Name)
			continue
		}
		if len(desc.RequiredFiles) == 0 {
			t.Errorf("%s 的 RequiredFiles 为空：ResolveSkill 会把任何目录都判为已安装", desc.Name)
		}
		// 未解析时必须按技能名引用，且不得凭空造出一条本机路径。
		unresolved := desc.PromptSection("")
		if !strings.Contains(unresolved, desc.Name) {
			t.Errorf("%s 未解析时的片段没提到技能名：%s", desc.Name, unresolved)
		}
		if strings.Contains(unresolved, string(filepath.Separator)+desc.Name) {
			t.Errorf("%s 未解析时不得写出本机路径：%s", desc.Name, unresolved)
		}
		// 解析后必须把本机绝对路径写进去，否则渲染器还得自己找。
		resolved := desc.PromptSection(filepath.FromSlash("/tmp/skills"))
		if !strings.Contains(resolved, filepath.Join("/tmp/skills", desc.Name)) {
			t.Errorf("%s 解析后的片段没有注入本机路径：%s", desc.Name, resolved)
		}
	}
}

// TestResolveAllSkillsReportsOnlyInstalledSkills 校验部分安装的情形：装了的进
// map，没装的缺席而不是报错。
func TestResolveAllSkillsReportsOnlyInstalledSkills(t *testing.T) {
	home := t.TempDir()
	installNamedSkill(t, filepath.Join(home, ".claude", "skills"), RegisteredSkills[0])

	resolved, err := ResolveAllSkills("claude", t.TempDir(), map[string]string{"HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 {
		t.Fatalf("只装了一个技能，却解析出 %d 个：%v", len(resolved), resolved)
	}
	if _, ok := resolved[RegisteredSkills[0].Name]; !ok {
		t.Fatalf("缺少已安装的 %s：%v", RegisteredSkills[0].Name, resolved)
	}
}

// TestTextToLottiePromptKeepsLottieOutOfTheMotionBudget 锁住这条边界：
// Lottie 与动效技能同时在提示词里，必须明确 Lottie 不顶替动效预算，
// 否则渲染器会拿一个 Lottie 图层充当整镜头的动效工作量。
func TestTextToLottiePromptKeepsLottieOutOfTheMotionBudget(t *testing.T) {
	for _, dir := range []string{"", filepath.FromSlash("/tmp/skills")} {
		section := textToLottiePrompt(dir)
		for _, want := range []string{"默认不用", "不计入", "环境动效"} {
			if !strings.Contains(section, want) {
				t.Errorf("skillsDir=%q 的片段缺少 %q：%s", dir, want, section)
			}
		}
	}
}

// TestBuildPromptInjectsEveryInstalledSkill 把注册表与提示词拼装接起来。
func TestBuildPromptInjectsEveryInstalledSkill(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	resolved := make(map[string]string)
	for _, desc := range RegisteredSkills {
		installNamedSkill(t, skills, desc)
		resolved[desc.Name] = skills
	}

	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, resolved)
	if err != nil {
		t.Fatal(err)
	}
	for _, desc := range RegisteredSkills {
		if desc.Name == SongExplainerSkillName {
			if strings.Contains(prompt, desc.Name) {
				t.Error("speech prompt includes song skill")
			}
			continue
		}
		if !strings.Contains(prompt, filepath.Join(skills, desc.Name)) {
			t.Errorf("提示词里没有 %s 的本机路径", desc.Name)
		}
	}
}

// TestBuildPromptDegradesToSkillNamesWhenNothingInstalled 校验降级：一个技能都
// 没装时提示词仍然成立，只是按技能名引用。
func TestBuildPromptDegradesToSkillNamesWhenNothingInstalled(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, desc := range RegisteredSkills {
		if desc.Name == SongExplainerSkillName {
			if strings.Contains(prompt, desc.Name) {
				t.Error("speech prompt includes song skill")
			}
			continue
		}
		if !strings.Contains(prompt, desc.Name) {
			t.Errorf("降级后提示词里没有 %s", desc.Name)
		}
	}
}

// TestHyperFramesCLIPromptForbidsRemoteAndSkillMutatingCommands 守着两条承重禁令。
//
// 上游 hyperframes-cli 技能覆盖 cloud / cloudrun / lambda / publish 等远端渲染
// 路径，以及 skills / upgrade 这类会改动已装技能的命令。把技能路径注入提示词
// 就等于把这些命令一并推到渲染器面前：
//
//   - 远端渲染绕开本机的确定性前提，产物规格与本地渲染不保证一致。
//   - 改动已装技能会顶掉 am 固定的版本，影响的是同项目的其他镜头，
//     而且失败会晚到成片阶段才暴露。
//
// 这两条只存在于我们自己写的提示词片段里，上游不会替我们守。
func TestHyperFramesCLIPromptForbidsRemoteAndSkillMutatingCommands(t *testing.T) {
	for _, dir := range []string{"", "/proj/.agents/skills"} {
		section := hyperFramesCLIPrompt(dir)
		for _, forbidden := range []string{"cloud", "lambda", "publish", "skills", "upgrade"} {
			if !strings.Contains(section, forbidden) {
				t.Errorf("skillsDir=%q 时，CLI 提示词没有点名禁止的命令 %q：\n%s", dir, forbidden, section)
			}
		}
		if !strings.Contains(section, "本机渲染") {
			t.Errorf("skillsDir=%q 时，CLI 提示词没有要求本机渲染：\n%s", dir, section)
		}
	}
}

// TestHyperFramesCorePromptRefusesWholeVideoPlanning：上游 core 技能同时覆盖
// 建子项目与 STORYBOARD.md / SCRIPT.md 计划格式，那是整片工作流的东西。
// 分镜由上层决定并已写进 scene.json，渲染器照那套走会产出计划文件并试图自己
// 排布多镜头，直接违反单镜头契约。
func TestHyperFramesCorePromptRefusesWholeVideoPlanning(t *testing.T) {
	for _, dir := range []string{"", "/proj/.agents/skills"} {
		section := hyperFramesCorePrompt(dir)
		for _, want := range []string{"STORYBOARD", "SCRIPT", "子项目", "一个 composition"} {
			if !strings.Contains(section, want) {
				t.Errorf("skillsDir=%q 时，core 提示词缺少 %q：\n%s", dir, want, section)
			}
		}
	}
}

// TestUpstreamSkillPromptsDegradeGracefully：解析不到目录时必须退回按技能名
// 引用。写死路径会在别的机器上指向不存在的位置，直接空掉则等于技能没注册。
func TestUpstreamSkillPromptsDegradeGracefully(t *testing.T) {
	for _, desc := range RegisteredSkills {
		if desc.Source != SourceUpstream {
			continue
		}
		fallback := desc.PromptSection("")
		if !strings.Contains(fallback, desc.Name) {
			t.Errorf("%s 的兜底片段没有点名技能：%s", desc.Name, fallback)
		}
		if strings.Contains(fallback, "/") && strings.Contains(fallback, "skills/") {
			t.Errorf("%s 的兜底片段里写死了路径：%s", desc.Name, fallback)
		}
	}
}

// 放行的 CLI 必须带固定版本前缀，与锁定文件要求一致；技能目录去重排序，
// argv 才稳定可比。
func TestRendererAccessPinsCLIAndOpensSkillDirs(t *testing.T) {
	access := RendererAccess(map[string]string{
		AnimationSkillName:       "/proj/.agents/skills",
		HyperFramesCoreSkillName: "/proj/.agents/skills",
		CharacterRigSkillName:    "/home/.claude/skills",
	})
	if access.Commands[0] != "npx --yes hyperframes@"+hyperframes.PinnedVersion {
		t.Errorf("第一条放行命令应是带固定版本的 hyperframes，实际 %q", access.Commands[0])
	}
	want := []string{"/home/.claude/skills", "/proj/.agents/skills"}
	if strings.Join(access.ReadDirs, ",") != strings.Join(want, ",") {
		t.Errorf("ReadDirs = %v，期望 %v", access.ReadDirs, want)
	}
}

// TestSceneKitPromptMakesCaptionsAndLayoutMandatory 守着 scene-kit 存在的理由：
// 措辞一旦退成「可以用」，渲染器照旧手写字幕与安全区像素，镜头之间字幕各不
// 相同，库就白装了。复制义务与加载顺序同样是承重的——引用镜头目录外的路径或
// 先于 am-scene.js 加载，字幕都会静默缺失而渲染照样成功。
func TestSceneKitPromptMakesCaptionsAndLayoutMandatory(t *testing.T) {
	for _, dir := range []string{"", filepath.FromSlash("/proj/.agents/skills")} {
		section := sceneKitPrompt(dir)
		for _, want := range []string{
			"强制", "SceneKit.captions", "SceneKit.safeRect", sceneKitLibraryFile,
			"复制进本镜头目录", "不得手写字幕代码", "先加载镜头目录里的 " + SceneDataFile,
		} {
			if !strings.Contains(section, want) {
				t.Errorf("skillsDir=%q 时，scene-kit 片段缺少 %q：\n%s", dir, want, section)
			}
		}
	}
}

// TestAnimationPromptDirectsEndStatesAndFollowRelations 钉住两条动效手艺要求：
// 只盯入场的动画常常落在一个说不清台词论点的终态上；跟随关系不成立（指针没落
// 在控件上、标签与对象脱节）则是画面自查最容易漏的错。
func TestAnimationPromptDirectsEndStatesAndFollowRelations(t *testing.T) {
	section := animationPrompt("")
	for _, want := range []string{"状态变化", "最终停在什么状态", "论点", "跟随关系", "指针落在它要按的控件上", "标签跟着它标注的对象走"} {
		if !strings.Contains(section, want) {
			t.Errorf("动效片段缺少 %q：\n%s", want, section)
		}
	}
}
