package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
)

// skillManifest 是每个技能的必需清单文件，与 scene 包的约定保持一致。
const skillManifest = "SKILL.md"

// skillTreeRoot 是技能树在项目根下的位置。scene.ResolveSkill 从镜头目录逐级
// 向上找的就是这个相对路径。
const skillTreeRoot = ".agents/skills"

// TestEveryRegisteredSkillIsEmbedded 是防静默失败的门。
//
// //go:embed 对目录模式会跳过 . 开头的条目，所以 assets/shared/.agents 必须在
// embed 指令里单列。漏了不会报错，只是整棵技能树从二进制里消失，用户项目里
// 什么都没有，而 ResolveSkill 找不到技能时按设计不报错、只降级——一路静默到
// 成片动效变差才可能被发现。这个测试让它在 CI 就炸。
func TestEveryRegisteredSkillIsEmbedded(t *testing.T) {
	shared, err := assets.Shared()
	if err != nil {
		t.Fatalf("取共享素材：%v", err)
	}
	for _, desc := range scene.RegisteredSkills {
		if desc.Source != scene.SourceEmbedded {
			continue // 上游技能由 am init 安装，不随二进制下发
		}
		for _, required := range desc.RequiredFiles {
			path := skillTreeRoot + "/" + desc.Name + "/" + required
			if _, err := fs.Stat(shared, path); err != nil {
				t.Errorf("内置技能树缺少 %s：%v", path, err)
			}
		}
	}
}

// TestBuiltinSkillsAreDocumentedInProjectRules 把内置技能与下发给渲染工具的
// 项目规则绑在一起。
//
// templates/project-rules.md 会被 WriteRules 派生成 AGENTS.md / CLAUDE.md /
// CODEBUDDY.md 写到生成项目根，是编排工具和每个渲染子进程都会读到的那份规则。
// 内置技能与联网安装的 HyperFrames 技能补救方式不同（前者重跑 am init，后者重装
// 官方版本），规则里没写清楚，渲染器缺技能时就会去做一件永远补不回来的事。
func TestBuiltinSkillsAreDocumentedInProjectRules(t *testing.T) {
	shared, err := assets.Shared()
	if err != nil {
		t.Fatal(err)
	}
	body, err := fs.ReadFile(shared, RulesTemplate)
	if err != nil {
		t.Fatalf("读 %s：%v", RulesTemplate, err)
	}
	rules := string(body)
	for _, desc := range scene.RegisteredSkills {
		if desc.Source != scene.SourceEmbedded {
			continue // 上游技能由 am init 安装，不在这份下发规则的职责范围内
		}
		if !strings.Contains(rules, desc.Name) {
			t.Errorf("%s 没有提到内置技能 %s：新增内置技能必须同步这份下发规则", RulesTemplate, desc.Name)
		}
	}
	// 锁定文件的读取例外必须点名技能树路径，否则「只在本镜头目录内工作」会把它挡在外面。
	if !strings.Contains(rules, skillTreeRoot) {
		t.Errorf("%s 没有点名 %s，镜头锁定文件会挡掉技能读取", RulesTemplate, skillTreeRoot)
	}
}

// TestInitializeDeliversSkillTree 校验技能树真的落到用户项目根，路径与
// scene.ResolveSkill 的项目级约定一致——两边任何一侧改路径都会让这里失败。
func TestInitializeDeliversSkillTree(t *testing.T) {
	target := filepath.Join(t.TempDir(), "video")
	if _, err := Initialize(target, builtinSources(t)...); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		skillTreeRoot + "/text-to-lottie/" + skillManifest,
		skillTreeRoot + "/text-to-lottie/LICENSE",
		skillTreeRoot + "/text-to-lottie/references/lottie-spec-map.md",
		skillTreeRoot + "/algorithmic-art/" + skillManifest,
		skillTreeRoot + "/scene-kit/" + skillManifest,
		skillTreeRoot + "/scene-kit/scene-kit.js",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(name))); err != nil {
			t.Errorf("项目里缺少 %s：%v", name, err)
		}
	}
}

// TestDeliveredSkillTreeIsDiscoverableFromSceneDirectory 把下发与发现两端接起来：
// 从一个真实深度的镜头目录出发，ResolveSkill 必须命中项目级技能树。
func TestDeliveredSkillTreeIsDiscoverableFromSceneDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "video")
	if _, err := Initialize(target, builtinSources(t)...); err != nil {
		t.Fatal(err)
	}
	sceneDir := filepath.Join(target, "scenes", "scene-001")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// HOME 指向一个没装任何技能的空目录，确保命中的是项目级而非家目录级。
	environ := map[string]string{"HOME": t.TempDir()}
	for _, desc := range scene.RegisteredSkills {
		if desc.Source != scene.SourceEmbedded {
			continue // 上游技能由 am init 联网安装，Initialize 不产出它们
		}
		dir, err := scene.ResolveSkill("claude", sceneDir, environ, desc)
		if err != nil {
			t.Fatalf("解析 %s：%v", desc.Name, err)
		}
		want := filepath.Join(target, filepath.FromSlash(skillTreeRoot))
		if dir != want {
			t.Errorf("%s 应解析到 %s，实际 %s", desc.Name, want, dir)
		}
	}
}

// TestSkillTreeDoesNotReferenceRepoLocalBinary 与提示词那条同源：用户项目里没有
// ./am，技能文件写了它就是一条死路径。
//
// 不检查 "agent" 一词——技能树的路径本身叫 .agents/skills，上游英文原文也合法地
// 提到 Agent Skills，这条只对下发给操作者的 PROMPT 文件成立。
func TestSkillTreeDoesNotReferenceRepoLocalBinary(t *testing.T) {
	forEachSkillFile(t, func(path string, text string) {
		if strings.Contains(text, "./am") {
			t.Errorf("%s 引用了 ./am，用户项目里没有这个二进制", path)
		}
	})
}

// TestForkedSkillCarriesNoUpstreamPlayerContract 守着 fork 的裁剪结果。
//
// text-to-lottie 的上游把场景绑死在它自己那个 Vite + Skia Skottie 播放器项目上
// （public/projects/ 布局、dev server、GET /__context）。本项目的镜头是
// HyperFrames 组合，由 am scene run 无头渲染，那套契约整体不适用。日后同步上游
// 时很容易把这些描述带回来，带回来渲染器就会去起 dev server 或写错路径。
//
// 只扫 references/：那是会被上游整份覆盖的部分。SKILL.md 与 ATTRIBUTION.md 是
// 本项目自己写的，它们**需要**点名这套契约来说明为什么不用它。
func TestForkedSkillCarriesNoUpstreamPlayerContract(t *testing.T) {
	banned := []string{"Skottie", "CanvasKit", "canvaskit", "public/projects", "__context", "npm run dev", "degit"}
	scanned := 0
	forEachSkillFile(t, func(path string, text string) {
		if !strings.Contains(path, "/references/") {
			return
		}
		scanned++
		for _, token := range banned {
			if strings.Contains(text, token) {
				t.Errorf("%s 含上游播放器契约残留 %q，见技能目录的 ATTRIBUTION.md", path, token)
			}
		}
	})
	if scanned == 0 {
		t.Fatal("没扫到任何 references/ 文件，检查技能树结构")
	}
}

// TestEverySkillDirectoryHasAManifest 防止只放了 references/ 却漏掉 SKILL.md——
// 那样 ResolveSkill 会判定未安装并静默降级。
func TestEverySkillDirectoryHasAManifest(t *testing.T) {
	shared, err := assets.Shared()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(shared, skillTreeRoot)
	if err != nil {
		t.Fatalf("读技能树：%v", err)
	}
	if len(entries) == 0 {
		t.Fatal("技能树是空的")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := skillTreeRoot + "/" + entry.Name() + "/" + skillManifest
		if _, err := fs.Stat(shared, path); err != nil {
			t.Errorf("技能 %s 缺少 %s", entry.Name(), skillManifest)
		}
	}
}

// forEachSkillFile 遍历内置技能树里的每个文本文件。
func forEachSkillFile(t *testing.T, check func(path string, text string)) {
	t.Helper()
	shared, err := assets.Shared()
	if err != nil {
		t.Fatal(err)
	}
	visited := 0
	err = fs.WalkDir(shared, skillTreeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(shared, path)
		if err != nil {
			return err
		}
		visited++
		check(path, string(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 {
		t.Fatal("技能树里一个文件都没遍历到，检查 embed 指令")
	}
}

// TestUpstreamSkillsAreNotVendoredIntoTheEmbeddedTree 是上一条的反面门禁。
//
// 把上游技能拷进嵌入树能让 TestEveryRegisteredSkillIsEmbedded 变绿，
// 但那会留下一份永远追不上上游的陈旧副本：am init 装的是固定版本的真品，
// 嵌入树里那份不参与任何升级流程，两者悄悄分叉后没有任何检查能发现。
// 上游技能只能由 am init 安装，不得进 assets/。
func TestUpstreamSkillsAreNotVendoredIntoTheEmbeddedTree(t *testing.T) {
	shared, err := assets.Shared()
	if err != nil {
		t.Fatalf("取共享素材：%v", err)
	}
	checked := 0
	for _, desc := range scene.RegisteredSkills {
		if desc.Source != scene.SourceUpstream {
			continue
		}
		checked++
		path := skillTreeRoot + "/" + desc.Name
		if _, err := fs.Stat(shared, path); err == nil {
			t.Errorf("上游技能 %s 被拷进了嵌入树（%s）：它不参与升级流程，会与上游悄悄分叉",
				desc.Name, path)
		}
	}
	if checked == 0 {
		t.Fatal("没有任何上游来源的注册技能，这条门禁形同虚设")
	}
}

// TestEverySkillHasAPromptSection：注册了却不注入提示词等于没注册——
// 技能装进项目，渲染器却不知道它存在。
func TestEverySkillHasAPromptSection(t *testing.T) {
	for _, desc := range scene.RegisteredSkills {
		if desc.PromptSection == nil {
			t.Errorf("技能 %s 缺少 PromptSection", desc.Name)
			continue
		}
		resolved := desc.PromptSection("/tmp/skills")
		if !strings.Contains(resolved, desc.Name) {
			t.Errorf("技能 %s 的提示词片段没有点名自己：%s", desc.Name, resolved)
		}
		// 解析不到目录时必须退回按名字引用，不能写死本机路径或直接空掉。
		fallback := desc.PromptSection("")
		if strings.TrimSpace(fallback) == "" {
			t.Errorf("技能 %s 在未解析到目录时产出空片段", desc.Name)
		}
		if strings.Contains(fallback, "/tmp/skills") {
			t.Errorf("技能 %s 的兜底片段里泄漏了路径", desc.Name)
		}
	}
}
