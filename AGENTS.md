# 仓库规则

本仓库是 ArticleToMotion 的 Go CLI 工具本体，交付物是单个 `am` 二进制。它**不是**视频项目：这里不制作视频，也不存放任何单片内容。给视频项目用的规则是 `assets/shared/templates/project-rules.md`，那份文件随项目骨架下发到用户项目的 `templates/project-rules.md`，不约束本仓库的开发。

## 命令

```bash
go build ./cmd/am
go test ./...
go test -race ./...
go vet ./...
```

## 内置资产

项目骨架分三棵源树，`assets.go` 的 `//go:embed` 清单是唯一来源：

| 源树 | 内容 |
|---|---|
| `assets/shared/` | 与画幅、风格都无关，所有项目共用（`PROMPT.md`、配置模板、中文正文字体、`templates/`、`.agents/skills/`） |
| `assets/presets/<画幅>/` | 只随画幅变化（`PROMPT-PRODUCTION.md`） |
| `assets/styles/<风格>/<画幅>/` | 风格 × 画幅（`frame.md`、风格说明书、示例图，以及该风格独有的字体） |

**每棵树的内部路径就是它在用户项目根下的目标路径**，`project.Initialize` 把选中的三棵树叠加拷贝，不做任何路径改写。所以 `assets/shared/assets/fonts/` 这层嵌套是刻意的——它对应项目里的 `assets/fonts/`。多棵树写入同一路径会直接报错，不静默覆盖；风格独有的字体要换一个文件名放进风格树，不能与共用树里的字体同名。

**新增一个需要随 `am init` 下发的文件时，要放进对的那棵树，并确认它落在 `//go:embed` 清单覆盖范围内**，否则它只存在于本仓库，永远到不了用户项目。`.env.example` 和 `.agents` 这类点开头的条目必须在 embed 指令里单列——目录模式会跳过它们，漏了不报错，只是那份内容从二进制里静默消失。

`am init` 不覆盖内容不同的已有文件：目标已存在且字节不同就整体失败并回滚，不做合并。

## 技能树

`assets/shared/.agents/skills/<name>/` 是随 `am init` 下发到用户项目根 `.agents/skills/` 的技能树。`scene.ResolveSkill` 从镜头目录逐级向上找 `.agents/skills`，落到项目根就能被发现，用户无需另行安装。

`hyperframes-animation` 等官方技能不在这棵树里，但**落点相同**：`am init` 通过 `internal/hyperframes` 把它们按固定版本装进同一个 `.agents/skills/`。这棵源树只放本仓库自带的技能。

官方技能不能直接跑 `npx hyperframes skills` 装——上游安装器只认 `homedir()`，没有任何选项能改落点（`skills update --dir` 的帮助原文写着 "scopes the prune, not the install"）。装在机器级会让项目之间互相覆盖：一台机器只有一份技能，项目 A 与项目 B 固定不同版本时谁后初始化谁说了算。

更要命的是上游安装器根本给不了可复现性：它 `git clone` 仓库默认分支取技能，没有指定 ref 的口子，技能内容随上游 `main` 漂移而版本号不变（实测 0.8.1 与 0.8.14 装出来逐字节相同）。所以 `internal/hyperframes` 不调用它，改为自己按 tag 浅克隆并校验 commit 与技能数量，见 `PinnedSkillsCommit` / `PinnedSkillsCount` 的注释；安装后写一份 `hyperframes-upstream.json` 记录 commit 与每个技能的内容摘要，见 `manifest.go`。

`internal/hyperframes` 的做法是在项目外开一个临时 HOME 让上游安装器照常工作，再把产出搬进项目。三个细节是承重的，改动前先读那里的注释：

| 细节 | 不这么做会怎样 |
|---|---|
| 用 `node -e process.execPath` 解析真实 npx | asdf / nvm / volta 的 shim 靠 HOME 找 node，改写 HOME 后直接报 `unknown command: npx` |
| 接回 `npm_config_cache` | 缓存写进临时 HOME，每个项目重新下载整包 |
| 接回 `npm_config_userconfig` | 读不到用户 `.npmrc`，私有 registry、代理和鉴权全丢，公司内网装不上 |

上游产物里出现符号链接时直接报错而不是照搬：那些链接指回临时 HOME，搬进项目后会成为静默失效的空技能。受保护技能名从嵌入树现读（`assets.BuiltinSkills`），新增内置技能自动受保护，不会被上游同名技能覆盖。

技能分两种来源，`SkillDescriptor.Source` 是它们的分水岭——不是元数据，而是契约：缺失补救、升级方式和守护测试全都不同。

| 来源 | 谁装 | 能否进嵌入树 |
|---|---|---|
| `SourceEmbedded` | 随二进制下发，`project.Initialize` 写进项目 | **必须**在 `assets/shared/.agents/skills/` |
| `SourceUpstream` | `am init` 按固定版本从上游装进项目 | **禁止**——那会留一份永远追不上上游的陈旧副本 |

两条门禁互为反面：`TestEveryRegisteredSkillIsEmbedded` 查前者在不在嵌入树，`TestUpstreamSkillsAreNotVendoredIntoTheEmbeddedTree` 查后者在不在。把上游技能拷进 `assets/` 能让第一条变绿，但会被第二条抓住。

新增一个**内置**技能要动的地方：

| 位置 | 作用 |
|---|---|
| `assets/shared/.agents/skills/<name>/SKILL.md` | 技能本体，`SKILL.md` 是必需文件 |
| `assets.go` 的 `//go:embed` 清单 | 已含 `assets/shared/.agents`，新技能自动覆盖 |
| `internal/scene/skills.go` 的 `RegisteredSkills` | 追加描述符，`Source: SourceEmbedded` |
| 同文件的 `<name>Prompt` 函数 | 该技能注入镜头提示词的片段 |
| `assets/shared/templates/project-rules.md` | 下发规则要点名它，有门禁强制 |

新增一个**上游**技能只要在 `RegisteredSkills` 加一条 `Source: SourceUpstream` 的描述符加提示词片段——它已经被 `am init` 装进项目了，注册只是让 `am` 把绝对路径和用法边界写进渲染提示词。

注册上游技能时要收窄范围，上游技能的职责范围通常比单镜头渲染大得多：

- `hyperframes-core` 同时覆盖建子项目与 `STORYBOARD.md` / `SCRIPT.md` 计划格式，那是整片工作流。分镜由上层决定并已写进 `scene.json`，不收窄的话渲染器会产出计划文件并试图自己排布多镜头。
- `hyperframes-creative` 同时覆盖选色板、选字体、`design-picker` 与整片旁白/节拍规划。本项目配色与字体由 `frame.md` 锁定，只让渲染器读构图与密度（`house-style.md`、`video-composition.md`）和写完后的对照清单（`design-adherence.md`）。
- `hyperframes-cli` 覆盖 `cloud` / `cloudrun` / `lambda` / `publish` 远端渲染路径，以及 `skills` / `upgrade` 这类改动已装技能的命令。前者绕开本机确定性前提，后者会顶掉固定版本、影响同项目其他镜头。

这两条禁令只存在于我们自己写的提示词片段里，上游不会替我们守，所以 `skills_test.go` 有对应的门禁盯着。**入口技能 `hyperframes` 刻意不注册**：它的职责是「选择并安装 owning workflow」，会把渲染器往整片制作上带。

`internal/project/skilltree_test.go` 守着这条链：注册了但没随二进制下发、下发了但镜头目录找不到、技能目录缺 `SKILL.md`，都会失败。**这些失败必须修，不能豁免**——`ResolveSkill` 按设计在找不到技能时不报错只降级，没有这道门就会一路静默到成片质量变差才被发现。

fork 自外部项目的技能要带 `LICENSE` 和 `ATTRIBUTION.md`，后者记清上游 commit、保留了什么、丢了什么、改了哪几处，供日后同步上游时重做。`text-to-lottie` 的 `references/` 还有一道门禁止上游播放器契约（`Skottie`、`public/projects` 等）回流。

## 画幅预设与风格

画幅与风格是两个正交维度，都在 `am init` 时各选一次（`--canvas`、`--style`）。画幅决定画布与安全区，风格决定配色、字体、版式骨架、动效语法与禁用项；`go generate` 把二者组合成每个风格 × 画幅的 `frame.md`：canvas 与 safe_area 两块来自画幅，其余 token 来自风格模板。

`assets/presets/<画幅>/` 与 `assets/styles/<风格>/<画幅>/` 下的**文本产物全部由 `go generate ./internal/preset/` 生成，不要手改**。真相源是 `internal/preset/table.go` 的画幅表、`internal/preset/style.go` 的风格表，以及 `internal/preset/gen/templates/` 下的模板（`PROMPT-PRODUCTION.md` 与风格无关；每套风格在 `templates/styles/<风格>/` 下各有 `frontmatter.yaml`、`frame.body.md`、`style-guide.body.md`）。安全区在预设表里用 anchor 声明（贴顶 / 贴底 / 上下固定），构建期推导成绝对像素写进 `frame.md`——anchor 不进项目文件，渲染工具读到的始终是现成像素值。

改完预设表后必须重跑 `go generate` 并提交产物，CI 的 `generated-assets` job 会校验二者一致。

示例 SVG 是手绘的排版基准，生成器不改它们的内容；改完 SVG 用 `go run ./internal/preset/gen -examples -style <风格> -preset <画幅>` 更新 PNG。PNG 不参与 CI 幂等门——不同版本的 `rsvg-convert` 输出字节不同，纳进来会让门随 runner 随机失败。

SVG 转 PNG 必须走 `rsvg-convert`（`internal/styleimage`），**不能用 ImageMagick**：多数 ImageMagick 构建自带内置 XML SVG 渲染器，会抢在 rsvg 委托前接管 `.svg`，渲染不出文字也画不对背景填充，却返回退出码 0。

新增画幅要动的地方：`internal/preset/table.go` 加一条（安全区、`Platform` 平台措辞，必要时加 `LayoutNotes` 构图要点）、为每套风格手绘 4 张该尺寸的示例 SVG、跑 `go generate` 与 `-examples`。`validate` 与 `scene.BuildPrompt` 都向预设表反查，不持有画幅常量，无需改动。

风格模板是所有画幅共用的，**随画幅变化的东西一律写占位符，不写死**：`{{CONTENT_LEFT_PX}}`、`{{CONTENT_WIDTH_PX}}`、`{{SUBTITLE_MAX_WIDTH_PX}}`、`{{COVER_MAX_WIDTH_PX}}` 由生成器从安全区推导（可带整数偏移，如 `{{CONTENT_LEFT_PX+8}}`）；`{{ORIENTATION}}`、`{{PLATFORM_UI}}`、`{{UI_ZONE}}`、`{{AVOIDANCE_ZONES}}` 取自画幅表的 `Platform`。在模板里写「竖屏」「抖音右侧互动栏」或 `904` 这类数字，横屏项目就会拿到错的规范。未知占位符会让生成器直接报错。

新增风格要动的地方：`internal/preset/style.go` 加一条；在 `templates/styles/<风格>/` 写三份模板（`style_id` / `style_name` 必须与风格表一致，生成器会校验）；为每个画幅手绘 4 张示例 SVG，放进 `assets/styles/<风格>/<画幅>/assets/style-guide/examples/`；跑 `go generate` 与 `-examples`。四种镜头骨架的 id（`preset.ArchetypeIDs`）是叙事职责，所有风格共用、顺序固定，风格只换名字、用途说明和示例图。`TestInitEveryStyleAndCanvasPassesStyleValidation` 会对每个风格 × 画幅跑一遍 `am init` 加 `validate style`。

### 自定义风格

`am init --style custom --style-brief "…"` 不套内置风格：只写入描述（`docs/style-brief.md`）和草稿树 `assets/styles/custom-draft/<画幅>/`。草稿由 `go generate` 产出（模板在 `templates/styles/custom-draft/`），字段齐全但 token 只是默认风格的占位值，没有示例图。定稿由编排提示词的「第一阶段补充：定风格」完成，`TestProductionPromptCarriesCustomStyleStage` 钉着那一节。

| 位置 | 约定 |
|---|---|
| `preset.CustomStyle` | 草稿身份（`style_id: custom-draft`）。**刻意不进 `builtinStyles`**——生成器、选择框和一串枚举内置风格的测试都遍历那张表，混进去会把草稿当成带示例图的正式风格 |
| `preset.IsCustomStyleID` | 自定义风格的命名空间 `custom-<小写短名>`。内置风格不得以 `custom-` 开头 |
| `validate.Style` | 草稿直接报「定风格阶段未完成」；定稿后说明书路径固定为 `preset.CustomStyle.GuideDoc()`，不从用户写的 `style_name` 拼；其余检查与内置风格相同 |
| `validate.RegenerateExamples` | 自定义风格直接渲染项目里的四张 SVG，不重着色、不改写；草稿也放行。`am validate style --regenerate-examples` 因此先出图再校验 |
| `scene.Load` | `style_guide` 仍是草稿时拒绝加载。`scene` 不依赖 `preset`，草稿 id 抄了一份常量，`TestDraftStyleIDMatchesPreset` 钉住两边一致 |
| `am style fonts` / `add-font` | 字体只能来自 `preset.AllFonts`（与 `assets/fontpool/` 双向一致，`TestFontPoolMatchesAllFonts` 守着）或用户自带 woff2 |

可选键 `mood_refs` 列氛围参照图，通常来自编排工具的内置生图（官方 Codex 的 `$imagegen`）。它**不查尺寸**——生图工具不能指定像素——也不当版式基准；`BuildPrompt` 会写明配色、字体、版式以 `frame.md` 为准。生图只在 ChatGPT 登录的官方 Codex 下才可能可用（API key 与第三方 provider 会被上游过滤），所以提示词把它写成可跳过的一步，不能变成定风格的前提。`CODEX_HOME` 为此进了安全模式白名单。

## Prompt 文件

`assets/shared/PROMPT.md` 和 `assets/presets/<画幅>/PROMPT-PRODUCTION.md` 是内置资产，不是本仓库的文档。它们随 `am init` 下发到项目根，被 `am run` 直接喂给编排工具。`PROMPT-PRODUCTION.md` 是生成产物，改它要改 `internal/preset/gen/templates/PROMPT-PRODUCTION.md`。

- 要像直接写给人类操作者的任务说明，不暴露执行者是 agent、Codex 或 AI，也不使用「主控 agent」这类身份设定；但可以明确说明他需要操作其他 AI。
- 不得出现 `./am` —— 用户项目里没有这个二进制，`am run` 通过 `PATH` 和 `AM_EXECUTABLE` 注入当前可执行文件。
- 这两条由 `internal/project/boundary_test.go` 的 `TestPromptsUseInstalledCLIAndDoNotExposeExecutorIdentity` 强制执行，违反会让测试失败。

## 媒体校验与拼接

`internal/mediaprobe` 是所有 ffprobe / ffmpeg 调用的唯一入口，别在其他包里直接拼这两个命令。

| 类型 | 作用 |
|---|---|
| `Spec` | 期望规格。**零值字段表示不检查**，调用方只想查一件事时不必填满全部字段 |
| `Spec.Check` | 返回**全部**不符项，不是第一条。每次只报一条会让调用方「修一条、重跑一次、再发现下一条」 |
| `Toolchain.Verify` | 完整校验流程，返回 `Report`。`error` 只表示「检查没跑成」，「跑了但不合格」是 `Report.OK == false` |
| `FrameStats` | 单帧灰度均值与标准差。`Blank()` 只看 `Flat()`，**不叠加 `Dark()`** |

空白帧阈值（`flatStdDev = 2.0`）刻意取得极保守。不能凭「真实帧标准差通常很高」来定：深色背景上只有一行标题时亮部占比不到 1%，标准差会低到 20 出头，那是合法封面。误判它比漏判一个真空白帧更糟——前者会让人很快不再信任这个检查。`TestFrameStatsDoesNotFlagSparseTextOnDarkBackground` 钉着这个上界。

`internal/concat` **刻意不提供重定时能力**。PROMPT 第八阶段写着「禁止通过 `setpts` 或改变帧率让镜头追赶配音」——提示词里的禁令可以被忽略，不存在的能力不能。所以分辨率或帧率不符归入 `Plan.Blocking` 要求重渲染，只有编码和像素格式差异走 `Plan.Normalize`。**不要为了「方便」给它加缩放或变速参数。**

镜头产物校验（`scene.VerifyOutput`）查画幅、帧率、时长、逐帧解码的总帧数、无音轨，不查编码和像素格式：PROMPT 第八阶段明确允许镜头产物规格不一致，由拼接前的规范化统一，在镜头层拦下会和那条既定流程打架。

校验失败返回 `*scene.VerificationError`，`cli` 把它包成 `schedule.Fatal` 停止重试。规格不符是确定性的，重跑只会得到同样的产物，而每次重试都是一次完整的 AI CLI 调用。**新增确定性失败时记得走同一条路。**

`scene.VerifyOutput` 还会跑 `internal/determinism` 的源码扫描：从合成入口沿本地引用展开，只扫渲染真正加载的文件，命中同样返回 `VerificationError`。规则只收「上游禁令里有、HyperFrames lint 不查、几乎没有合法用途」的写法；`setTimeout` 刻意不收，等字体就绪的代码里太常见。新增规则前先拿内置技能与上游技能的示例跑一遍——`TestEmbeddedViewerTemplatePasses` 钉着 algorithmic-art 的 viewer 模板，它的交互路径靠逐行 `am-allow-nondeterministic` 豁免。误判一次，这道门就没人信了。

`@font-face` 只判两种确定的错误：`src` 指向镜头目录里不存在的文件，以及 `font-display` 显式写成 `swap` / `fallback` / `optional`。没写 `font-display` 不判（Chrome 的 `auto` 按 `block` 处理），`local()` 不判（常见写法是 `local()` 后接 `url()` 兜底）。

成片级检查有两条：`am review` 比对音视频流时长、总帧数与镜头帧数之和、镜头交界帧并出联系表，并解码成片实测积分响度与真峰（只进 hints：平台响度规范各不相同，frame.md 的目标是缺省建议）；`am audio check` 用能量包络独立校验字幕时间轴。两者把切黑转场、字幕延长停留这类「机器分不清有意还是事故」的情况放进 hints，不改变退出码。

`am validate reads` 读 `visible-event-inventory.json` 检查每个可读元素的停留时长、短语绑定与顺序。阈值取 Netflix 简体中文字幕规范（每秒 9 字、每条至少 5/6 秒），那是固定位置字幕的下限；画面元素还要先被找到，所以只能放宽不能收紧——收紧会把合法的快节奏镜头判死。字数按阅读单位算（一个英文词算一个），同理是为了不高估。两个 JSON 的字段名写死在制作提示词第四阶段，改字段要同步改那里。

`mediaprobe` / `concat` 的集成测试需要真实 ffmpeg，缺失时自行跳过。CI 装了 ffmpeg——不装的话测试仍然"通过"但覆盖率掉到门禁线以下，真正跑外部命令的代码再没被验证过。

## 镜头时长、局部数据与复用

配音时间轴由 `am align`（`internal/align`）从终稿与对齐器输出算出：终稿用 `<显示|朗读>` 与 `@{id}…@{/id}` 标记，`build` 逐字匹配（标签 exact / split / merge / replacement / source-omission / evidence-insertion），写 `voice.srt` 与 `phrase-timeline.json`。匹配代价刻意不照搬 HYPIT：它按整词匹配，按单字匹配时原样的代价会把漏读的几个字合并成一次「替换」藏起来，见 `match.go` 的注释。对齐器 JSON 的结构没有逐版本实测，加载器按「找到第一组带 text/start/end 的对象数组」宽松解析，认不出时报出顶层结构并给出标准结构 `[{"text","start","end"}]`。人工修正只走 `phrase-adjust.json`，不改测量数据。

口播项目的镜头时长由 `am scene cues` 从分镜计划 `production/scene-plan.json` 推出，与卡点模式的 `am beat cues` 同一个思路：计划只写锚点（`first_subtitle` / `first_phrase`）与切点规则（`cut`），帧号由代码算，写进 `scene.json` 的 `duration_seconds` 与 `timing` 块，`scene.VerifyTimingInputs` 做精确比对。秒换帧沿用 `beat.FrameOf`，不要另写取整。计划严格解析，新增字段要在 `scene.PlanEntry` 里登记，否则提示词里让编排工具写的键会让命令直接失败。

`scene.Run` 在启动渲染器前写 `am-scene.js`（`window.AM_SCENE`）。它的字段名是 `scene-kit` 技能的契约：`scenedata.go` 与 `assets/shared/.agents/skills/scene-kit/scene-kit.js` 两边改一处就要改另一处，`internal/project/scenekit_test.go` 用真实扫描器把示例镜头跑一遍。没有 `timing` 块的老镜头照样写这份文件（`cues` / `phrases` 为空），提示词里不出现时间合同表。

镜头交接（`timing.go` 的 `PlanHandoff` / `Handoff`）是分镜计划里唯一跨镜头的约定。渲染器互相看不到，交界能接上只靠两侧拿到**同一份**约定，所以它由 `am scene cues` 从计划抄进两侧的 `timing.enter` / `timing.exit`，不让编排工具在两份 `prompt.md` 里各写一遍。手法表 `HandoffDevices` 刻意只收「靠画面里的东西接过去」的写法，不收通用擦除——章节交界套固定转场正是要消除的生硬；「相邻章节交界不得同手法」也是同一个理由。`am review` 用 `mediaprobe.FrameDifference`（32×32 RGB 均值绝对差）比对声明过交接的交界，阈值取得宽，只拦「两张不同的画面」：物件挪几像素就报的话，这道门很快没人信。改手法表要同步改制作提示词第五阶段「镜头交接」的表格，`TestProductionPromptCarriesSceneHandoffs` 钉着。

镜头复用按输入指纹判定（`scene.Fingerprint`，记录在镜头目录的 `.am-render.json`）。指纹只算**声明过的输入**，不扫整个镜头目录——渲染器自己会往目录里写合成代码，算进去的话渲染一结束指纹就变了。镜头新增一类输入文件时，要把它加进 `Fingerprint` 的文件清单，否则改了它也不会触发 Stale。指纹一致只表示「这份已验收产物出自当前输入」，不表示可复现；指纹不一致仍只标 Stale、要 `--force` 才覆盖，不要改成自动重渲染。

每次渲染写 `render-<id>.attempt.json`，重渲染前 `scene.ArchiveAttempt` 先把上一次的产物、日志与尝试记录挪进 `attempts/`。调度层的重试与单镜头重跑走同一个归档函数，不要各写一份编号逻辑。token 用量按各工具流式事件尽力读取（`tools.AccumulateUsage`），读不到就留空，不做估算。

## 卡点模式

`internal/beat` 是无配音卡点的时间契约，以项目根 `beat.yaml` 启用，与 `song.yaml`、`cast.yaml` 互斥。链路是「导入音乐 → 分析出草稿网格 → 试听确认 → 按小节的分镜计划 → `am beat cues` 写镜头时长」。

| 约定 | 原因 |
|---|---|
| 切点帧号只由 `beat.FrameOf`（秒 × fps 四舍五入）算出，总帧数只由 `beat.TotalFrames` 算出 | 两处取整方式不同，画面与音乐会慢慢错开，而且是那种看半天说不出哪里不对的错开 |
| 分镜计划 `plan.json` 只接受小节数，不接受秒数 | 能写秒数就能写出不在重拍上的切点；切点卡在重拍上要由结构保证，不靠事后对轴 |
| `am beat cues` 写 `duration_seconds`，`beat.VerifyCue` 对切点做**精确**比对 | 帧号只有一个来源，任何偏差都是有人手改了时长；成片里的 ±1 帧容差属于整片复查，不属于这里 |
| 草稿网格永远不直接生效，只有 `beat.Confirm` 能把 `reviewed` 置为 true | 模型最常见的错是重拍整体错位或拍号判错，只有听过试听轨才知道 |
| 分析脚本把结果写进文件，不打印到 stdout | 首次运行下载模型权重，进度输出会污染标准输出，按 stdout 解析时结果直接丢失（实测过） |
| 音乐由 ffmpeg 解码成单声道 WAV，脚本用标准库 `wave` 读，再调 `Audio2Beats` | 不走 beat-this 自带的 torchaudio 读文件：它的后端随版本变化，能不能读 MP3 取决于用户机器 |

节拍分析固定 `beat-this` `AnalyzerVersion`（代码与权重 MIT），不用 librosa（没有重拍）和 madmom（Python 3.12+ 兼容问题，DBN 后处理有专利、限非商用）。CI 不装 torch，真实分析的集成测试只在设置 `AM_TEST_BEAT_PYTHON` 时运行，改动分析脚本后要在本机跑一遍。

镜头的 `beat` 块与 `song` 块并列，`scene.VerifyBeatInputs` 在渲染前、复用已有产物前与 `run-all` 里都会执行；启用了卡点却没有 `beat` 块的镜头直接拒绝。

## 平台封面

`publish.md` 的 `cover.images` 登记上传用的平台封面，校验在 `internal/validate/cover_images.go`，平台规格只有一个来源：`coverSpecs`（抖音 4:3 + 3:4、小红书 3:4 / 1:1 / 4:3、视频号 3:4 / 16:9、B 站与知乎 16:10，以及各平台 `ready` 前必须备齐的比例）。平台规则变了改这张表，制作提示词第十二阶段「平台封面」的表格要同步改，`TestProductionPromptRequiresPlatformCovers` 钉着那一节。

尺寸以文件实测为准、声明值只用来对账：手填宽高与实际不符，说明封面改过却没回填，发布时上传的就不是记录里那张。短边下限 1080，所以 B 站常见的 1146×717 不合格（它本身也不是精确的 16:10），用 1920×1200。模板里写封面尺寸时用「宽 1440、高 1080」，不要写成 `1440×1080` 这类乘号形式——`TestRenderSubstitutesCanvasLabel` 会把它当成写死的画幅拦下。

`safeRelative` 解析路径时找最近一层已存在的上级目录（`resolveExisting`），不只看直接父目录：目标在还没建出来的子目录里（例如还没出图的 `covers/`）时，只解析父目录会因为 macOS 临时目录的 `/var` → `/private/var` 符号链接而误判成逃出项目。

## 公开树边界

`TestPublicTreeContainsNoPrivateOrLegacyContent` 扫描 `git ls-files --cached --others --exclude-standard`，命中即失败。禁止出现的路径包括 `transcription.srt`、`final.mp4`、`publish.md`、`scenes/`、`production/`、`auto-test/`、`article_to_motion/`、`pyproject.toml`、`am`。

调试产生的单片内容留在仓库外，不要靠 `.gitignore` 遮掩——未跟踪文件同样会被这个测试扫到。

## 新增一个 AI CLI

工具矩阵散落在四个文件，加一个工具必须五处同步改，漏一处会在运行期才炸：

| 位置 | 作用 |
|---|---|
| `internal/tools/tools.go` `RendererInvocation` | 单镜头渲染的命令行与非交互权限模式；该模式若拦截 Bash，要把 `RendererAccess` 译成该工具的放行配置（claude 用 `--settings`） |
| `internal/tools/tools.go` `OrchestratorInvocation` | 编排调用的命令行与 prompt 传递方式（stdin 还是参数） |
| `internal/config/config.go` `validTools` | `ORCHESTRATOR` / `RENDERER` 取值校验 |
| `internal/scene/scene.go` `validRenderer` | `scene.json` 的 `renderer` 字段校验 |
| `internal/scene/skills.go` `rendererSkillDirs` | 该工具的技能目录约定（家目录级与项目级） |

同时更新 `assets/shared/article-to-motion.conf`、`assets/shared/.env.example`、`README.md`、`README.en.md` 里的可选值注释。

安全模式的权限模式必须实测能跑通渲染命令。claude 的 `acceptEdits` 只自动批准文件编辑、拦下所有 Bash，而 `-p` 下没人批准，渲染器会执行不了 `npx hyperframes render`；`scene.RendererAccess` 列出要放行的最小命令集（带固定版本的 hyperframes、`ls`、`ffprobe`）与技能目录读取，`.env` 始终禁读。

## 新增一个 TTS 或对齐工具

配音与对齐由编排工具按提示词执行，`am` 只登记取值并做与工具无关的校验。取值表在 `internal/config/providers.go`（`TTSProviders` / `Aligners`），校验与 `am config get` 的帮助都从它派生。加一个工具要同步：

| 位置 | 作用 |
|---|---|
| `internal/config/providers.go` | 取值、名称、自带时间戳粒度 |
| `internal/preset/gen/templates/PROMPT-PRODUCTION.md` 第三、四阶段 | 安装、登录、合成与取时间戳的具体命令，改完跑 `go generate` |
| `assets/shared/article-to-motion.conf`、`.env.example`、两份 README | 可选值注释 |

收录门槛是「有可非交互调用的 CLI，且凭据不必落进项目文件」。写进提示词的命令要实测过；跑不了的（没有账号）照官方文档写，并让编排工具先打印返回结构再解析。没有自带字级时间戳的路径，`voice.srt` 一律由强制对齐生成，不拿句级时间戳凑。

## 子进程环境

`Config.ChildEnvironment` 在安全模式下只透传白名单变量。`USER` / `LOGNAME` / `SHELL` 不是凭据但必须保留——macOS 上 claude CLI 缺 `USER` 就读不到 Keychain 登录态，报错却是「Not logged in」，与权限隔离毫无关联，极难排查。

新增白名单项要写清楚理由；需要临时透传由用户通过 `AM_PASSTHROUGH_ENV` 决定，不要在代码里放宽默认。
