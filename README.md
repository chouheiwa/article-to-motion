<p align="right">简体中文 | <a href="./README.en.md">English</a></p>

# ArticleToMotion

ArticleToMotion 是面向 macOS 和 Linux 的竖屏 MG 视频生产 CLI。它可以把定稿 SRT 拆成并发渲染的动画镜头，也可以从文章或口播稿完成 TTS、字幕、封面、声音制作和发布交付。

▶ [观看 ArticleToMotion 宣传视频（MP4，4 分 47 秒）](https://github.com/chouheiwa/articale-to-motion/blob/main/docs/article-to-motion-tutorial.mp4)

Go 版本以单个 `am` 二进制发布，不依赖 Python。项目 Prompt、发布模板和视觉规范由二进制内置；HyperFrames 技能在初始化时按固定官方版本装进项目，项目因此自包含——整个目录拷到另一台机器就能渲染。

## 安装

从 GitHub Releases 下载适合平台的压缩包，校验 `checksums.txt` 后把 `am` 放入 `PATH`。运行需要：

- macOS 或 Linux（amd64/arm64）
- Node.js 22+
- Git、FFmpeg、FFprobe
- Codex、Claude Code、Qoder、CodeBuddy、OpenCode 中至少一个已登录的 CLI

## 创建项目

```bash
am init my-video                          # 交互选择画幅
am init my-video --canvas vertical-9x16   # 或显式指定
cd my-video
```

画幅在初始化时一次性选定，写入项目的 `frame.md` 后不再更改。当前内置：

| 预设 | 画幅 | 用途 |
|---|---|---|
| `vertical-3x4` | 1080×1440，30fps | 默认，知识与科技类竖屏解说 |
| `vertical-9x16` | 1080×1920，30fps | 抖音等全屏竖屏平台 |
| `landscape-16x9` | 1920×1080，30fps | B 站、YouTube 等横屏平台 |

横屏的安全区避让底部播放器控制栏与进度条（竖屏避让的是抖音右侧互动栏），`frame.md` 末尾附有横屏构图要点。12 套风格都同时支持竖屏和横屏。

非交互环境（CI、管道）必须显式传入 `--canvas`，不会静默取默认值。

### 视觉风格

风格与画幅正交，同样在初始化时一次性选定：画幅决定画布与安全区，风格决定配色、字体、版式骨架、动效语法与禁用项。用 `--style` 指定，不传时在终端里交互选择；非交互环境不传则取默认的清晰系统蓝图。

```bash
am init my-video --canvas vertical-9x16 --style pixel-arcade-v1
```

| 风格 | 名称 | 适合 |
|---|---|---|
| `clear-system-blueprint-v1` | 清晰系统蓝图 | 默认；知识、技术与产品机制讲解 |
| `pixel-arcade-v1` | 像素街机 | 8/16-bit 复古游戏、怀旧、入门科普 |
| `scifi-hud-v1` | 科幻 HUD | 航天、硬件、系统架构、任务式讲解 |
| `cyberpunk-glitch-v1` | 赛博故障 | 安全攻防、黑客叙事、反差观点 |
| `candy-casual-v1` | 糖果休闲 | 轻松科普、生活技巧、成长激励 |
| `rebel-graphic-v1` | 二次元锐利拼贴 | 强观点、排行、角色化表达 |
| `fantasy-quest-v1` | 奇幻羊皮卷 | 历史、故事、学习路线、世界观 |
| `esports-broadcast-v1` | 电竞赛事转播 | 对比评测、排行榜、数据战报 |
| `visual-novel-v1` | 视觉小说对话 | 故事、对话体、情感与人物 |
| `neon-data-dark-v1` | 暗夜数据霓虹 | AI、前沿技术、数据与指标 |
| `editorial-magazine-v1` | 杂志编辑排版 | 观点、人文、商业评论 |
| `whiteboard-doodle-v1` | 白板手绘 | 教程、概念拆解、课堂讲解 |

每套风格写入 `frame.md`、`docs/<风格名>-视频风格说明书.md` 与四张版式示例图，并把该风格用到的字体（均为 SIL OFL 1.1，许可证随附）拷进 `assets/fonts/`。

`am init` 不覆盖内容不同的已有文件。默认联网安装固定版本 0.8.14 的 HyperFrames 官方技能到项目的 `.agents/skills/`，与随二进制下发的 `text-to-lottie`、`algorithmic-art` 同处一个目录；安装依赖 `git`，离线或 CI 环境可使用 `--skip-hyperframes`。

技能装进项目而不是 HOME，是因为项目之间互不覆盖只有这样才成立：上游安装器只认 `homedir`，一台机器上只有一份技能，项目 A 与项目 B 固定不同版本时谁后初始化谁说了算。安装全程不写用户 HOME——上游安装器在项目外的临时目录里运行，产物再搬进项目。

#### 技能安装为什么不走上游安装器

上游 `hyperframes skills` 直接 `git clone` 仓库的**默认分支**再取技能，没有任何指定 tag 或 commit 的口子。实测同一台机器上 `hyperframes@0.8.1` 与 `hyperframes@0.8.14` 各装一遍，产出的 26 个技能逐字节相同、都等于当天 `main` 的状态——版本号固定的只是 CLI 二进制，技能内容会随上游主干漂移，而技能内容直接决定成片动效。

所以 `am init` 不调用它，改为自己按 tag `v<版本>` 浅克隆上游仓库，从 `skills/` 与 `.agents/skills/` 两处取出含 `SKILL.md` 的目录。实测这套筛选在同一个 commit 上与上游安装器的产出**逐字节相同**。三道闸保证结果确定：

- **commit 校验**：tag 可以被 force-push 移动，解析出的 commit 与 `PinnedSkillsCommit` 不符就拒装。
- **数量校验**：上游哪天挪动目录，按目录拼装会静默少装一批技能，而渲染 agent 找不到技能不会报错、只会自己发明写法。数量与 `PinnedSkillsCount` 不符就拒装。
- **环境隔离**：`GIT_LFS_SKIP_SMUDGE=1`（仓库里有 LFS 媒体文件，装了 git-lfs 的机器会还原成真文件、没装的留下指针，同一 commit 在两台机器上得到不同的树）、`GIT_CONFIG_GLOBAL/SYSTEM=/dev/null`（用户的 filter、autocrlf 不能参与检出）。

`am init` 还会把结果写进 `.agents/skills/hyperframes-upstream.json`：版本、上游 commit，以及每个技能目录的内容摘要。升级固定版本时改 `internal/hyperframes` 里的 `PinnedVersion` / `PinnedSkillsCommit` / `PinnedSkillsCount` 三个常量。

技能安装依赖 `git`（不再依赖 Node/npx）；离线或 CI 用 `--skip-hyperframes` 跳过。

初始化还会把二进制内置的技能树写到项目的 `.agents/skills/`，渲染工具会自动发现：

| 技能 | 用途 |
|---|---|
| `text-to-lottie` | 镜头里的 Lottie 图层：logo 演绎、图标、loader、SVG 描边、矢量特效 |
| `algorithmic-art` | 算法与生成式视觉效果 |

这两个随二进制下发，不需要联网安装，`--skip-hyperframes` 也不影响它们。

编辑 `article-to-motion.conf` 选择编排工具与渲染工具；两者可以相同：

```text
ORCHESTRATOR=codex
RENDERER=claude
TTS_PROVIDER=minimax
ALIGN_PROVIDER=auto
SCENE_JOBS=3
```

`TTS_PROVIDER` 可选 `minimax`、`bailian`、`edge`（免鉴权，适合预览）、`elevenlabs`、`tencent`、`mlx`（本机离线，仅 Apple Silicon）；凭据一律走各工具自己的登录命令。`ALIGN_PROVIDER` 选择已知文本强制对齐工具，缺省 `auto`：Apple Silicon 上用 mlx-audio 的 Qwen3-ForcedAligner，其余平台用 FunASR。没有自带字级时间戳的 TTS 路径，字幕时间轴由它生成。

优先级为环境变量、项目 `.env`、配置文件、内置默认。项目 `.env` 不允许保存 API Key、Token、Secret 或 Password。

画幅之外，叙事模式是 `am init` 的第二个一次性维度：`--narration cast` 额外写出 `cast.yaml` 与 `cast/` 目录，进入多角色对话模式，见下方「多角色叙事」；不传则维持默认的单口播模式，行为与之前完全一致。

## 工作流

已有定稿 SRT 时，将它保存为 `transcription.srt`，然后运行：

```bash
am run
```

从文章、口播稿或参考字幕开始完整制作：

```bash
am run PROMPT-PRODUCTION.md
```

`am run` 会自动将当前 `am` 可执行文件所在目录放到编排子进程的 `PATH` 首位，并注入其绝对路径为 `AM_EXECUTABLE`。因此 Prompt 中的 `am scene ...` 会继续使用启动本次流程的同一个 CLI，无需把二进制复制进项目。

镜头命令也可独立使用：

```bash
am scene run scenes/scene-001
am scene run-all scenes/ --jobs 3 --retries 2 --report-json production/run-report.json
```

已有合格产物会跳过；输入更新后的产物标记为 stale，使用 `--force` 明确重渲染。中断时停止新任务、终止在飞进程组，并写出部分报告。

## 多角色叙事

用 `am init --narration cast` 建的项目里，`cast.yaml`（登记班底）与 `cast/` 目录的存在本身就是模式开关：老项目没有这两样东西，行为一个字不变。

一个角色包是 `cast/<id>/` 下的一个目录：`character.yaml` 是唯一真相源（音色、rig 关节、姿势、可选的多视图与转身路径），配一份（或每个视图各一份）`rig.svg` 骨架图，`dna.md` 写人设供编排 agent 读、不参与渲染。

```bash
am cast new heiwa                  # 生成角色包骨架并登记进 cast.yaml（voiceId 留空待填）
am cast add ../shared-characters/heiwa   # 引入外部角色包：拷贝进 cast/ 并登记进 cast.yaml
am cast validate                   # 校验 cast.yaml 登记的全部角色包，重新生成 character.json
am cast preview cast/heiwa         # 每个命名姿势渲一张 contact sheet，人眼验收角度是否合理
```

手改 `character.yaml` 后不会自动同步：`character.json` 只在 `new`/`add`/`validate` 成功时重新生成，渲染机读取的是后者，改完一律先跑一次 `am cast validate`。这条命令还会顺带提醒当前 `TTS_PROVIDER` 是否缺音色（不影响退出码，供作者在做完角色包的第一时间就看到）；发布前的硬校验在 `am validate cast`，见下方「校验与归档」。

分说话人合成的配音需要重建成一条时间线，再按拆定的镜头切成节拍——这两步都是"算错了不会报错，只会让成片嘴和字对不上"的机械计算，因此收进 Go：

```bash
am dialogue assemble               # 读 production/audio/plan.json，拼出 voice.wav / SRT / dialogue.json
am dialogue beats                  # 镜头拆定后，按 dialogue.json 重算各 scene.json 的 cast.beats
```

作业规程（对话体脚本怎么分段、镜头 `scene.json` 的 `cast` 块怎么写）随项目下发在 `PROMPT-CAST-ADDENDUM.md`，`cast.yaml` 存在时生效。

## 安全模式

外部 AI CLI 默认限制在项目工作区。若某个已安装版本不支持受限的非交互模式，命令会失败关闭，不会自动扩大权限。

只有确认项目和输入可信时才使用：

```bash
am --unsafe run
```

`--unsafe` 会传递给嵌套镜头任务。安全模式需要额外传递的环境变量通过 `AM_PASSTHROUGH_ENV=NAME1,NAME2` 显式列出。

## 视觉检查与拼接

渲染成功不等于画面正确：文字压在图形上看不清、中文字体静默回退成方框、动画在中途就停住，这些都能拿到退出码 0。抽帧看图是唯一能提前发现它们的手段。

```bash
# 抽帧供视觉验收，--at 支持秒数、百分比与 end
am scene frames scenes/scene-001 --at 0,50%,end
am scene frames scenes/scene-001 --at 0 --check-blank   # 空白封面直接判失败

# 静态扫描合成源码的确定性问题（渲染后的产物校验会自动跑同一套规则）
am scene lint scenes/scene-001

# 按镜头顺序拼成静音母版，规格一致时全程 -c copy
am concat scenes/ --out production/silent-master.mp4 --dry-run
am concat scenes/ --out production/silent-master.mp4
```

`am concat` 不提供重定时能力。分辨率或帧率与母版不符时直接失败并要求回镜头工程重渲染，而不是缩放或改帧率——前者损失画质，后者会动到时间轴。

镜头产物校验除了画幅、帧率、时长与无音轨，还会扫描渲染实际加载的 HTML/JS/CSS：读时钟、`Math.random()`、`requestAnimationFrame`、`setInterval`、无限 `repeat`、渲染时联网、在线字体与未固定版本的 CDN 库都会让同一帧在两次渲染间不同，命中即判失败且不重试。渲染路径确实不经过的交互代码可在同一行注释写 `am-allow-nondeterministic: 理由` 豁免，豁免项照样写进报告。

`am scene run-all` 按镜头时长从长到短派发，避免排在末尾的长镜头独自拖长整批耗时；并发渲染时按 `(CPU 核数 - 2) / 并发数` 给每个镜头分配 HyperFrames 的 `--workers`，免得几个渲染各自按独占整机估算、合起来超订 CPU 和内存。

## 校验与归档

```bash
am validate publish publish.md --project-root .
am validate style --project-root .
am validate style --project-root . --regenerate-examples  # 需要 rsvg-convert 与 magick

# 多角色项目：班底、对白时间线（dialogue.json）与镜头节拍是否互相吻合；
# 单口播项目（没有 cast.yaml）自动跳过，退出码 0
am validate cast --project-root .

# 成片机器验收：画幅、帧率、编码、像素格式、帧数、音轨一次全查，不符项一次列全
am validate video production/silent-master.mp4 --silent --decode
am validate video final.mp4 --audio --expect-frames 8340 --check-frame-zero \
  --report-json production/final-check.json

# 字幕时间轴与配音逐条比对：用能量包络独立判定每一刻有没有声音，拦下整体偏移与漏字幕
am audio check --audio production/audio/voice.wav --srt production/audio/voice.srt

# 整片复查：音画时长、总帧数与镜头帧数之和、镜头交界帧，并按镜头生成联系表
am review final.mp4 --scenes scenes/ --out production/review

am archive --dry-run
am archive
```

`--expect-frames` 与 `--decode` 会完整解码一遍，代价与文件长度成正比，所以默认关闭。判断结果读 `--report-json` 落盘的文件，不要解析终端输出。

归档在共享文件有修改或存在 ignored 文件时拒绝执行。成功后单片文件连同 SHA-256 清单移到仓库外归档目录，工作区 detach 到冻结的 `main` 提交。

## 开发

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/am
```

项目采用 [Apache License 2.0](./LICENSE)。HyperFrames 是独立的 Apache-2.0 项目，ArticleToMotion 初始化时使用固定版本，不在本仓库复制本机技能目录。

内置技能 `text-to-lottie` 的 `references/` 裁剪自 [diffusionstudio/lottie](https://github.com/diffusionstudio/lottie)（MIT），版权声明与改动记录见 `assets/shared/.agents/skills/text-to-lottie/` 下的 `LICENSE` 与 `ATTRIBUTION.md`。

## 可选：单人歌曲讲解

默认口播和多角色流程不变。歌曲模式先生成候选，用户试听选定后再对齐、分镜；
首版不支持歌曲与 cast、口播混排。`am` 仍是单个 Go 二进制。

```bash
am init my-song --canvas vertical-3x4 --delivery song
cd my-song
# 查看平台开通要求、密钥申请入口与配置方法
am song providers
am song configure --provider fal       # 示例：托管 ACE-Step，无需部署
# 按输出在本机设置 FAL_KEY，再编辑 lyrics.txt 与 song.yaml
am song doctor
am song generate                       # 默认两首，串行；完成后停止供用户试听
am song select <candidate-id>           # 由用户确定
am song prepare
# 核对唱词、术语和句首时间；有异常先修正报告旁的 timeline-draft.json
am song prepare --timeline production/song/timeline.json --reviewed
# 按实际音频制作 scene.json，镜头时长均为整数帧
am song cues scenes/
am validate song
am scene run-all scenes/ --song-timeline production/song/timeline.json
am concat scenes/ --out production/silent-master.mp4
am song mux production/silent-master.mp4 --out final.mp4
```

已有项目运行 `am song init`，只补歌曲配置、提示词和技能，不覆盖不同内容的文件。
`am run` 检测 `song.yaml` 后使用歌曲入口；显式提供其他提示词仍追加歌曲契约。
外部歌曲用 `am song import song.mp3 --lyrics lyrics.txt` 导入，再试听选定。

### 网页生成，提供音频后继续

没有 API 权限也能使用。选择网页模式后，`am run` 根据文章创作歌词和风格，
展示两段可复制文本并暂停，等待你在网页生成歌曲，再上传 MP3/WAV 或给出本机路径。
同一会话可以继续，也可以再次运行 `am run` 读取交接记录续做，无需一直保持进程运行。

```bash
am song configure --provider manual
# 写好 lyrics.txt 和 song.yaml 中的 style 后：
am song handoff
# 在网页生成、下载，再接收文件：
am song receive /path/to/downloaded-song.mp3
# 用户试听确认后才 select；随后 doctor、prepare 和原有视频流程
am song select <candidate-id>
```

`manual` 模式下 `am song generate` 也只输出交接材料，不请求 API。
歌词与风格快照在 `production/song/handoffs/`，等待/接收状态在 `web-handoff.json`。
接收使用交接时的歌词，不受之后编辑项目歌词影响；等待期间要改材料，需明确运行
`am song handoff --refresh` 并在网页重新生成。网页若修改了歌词，请提供最终歌词并用
`am song import AUDIO --lyrics FINAL_LYRICS` 导入。上传文件不等于确认选定，仍需试听确认。

`am song providers [平台]` 无需项目即可查看申请入口、模型、权限限制与密钥状态；
`am song configure` 只修改平台相关设置，保留歌词、曲风和 YAML 注释，不保存密钥、不产生调用费用。

| 平台 | 配置命令 | 本机环境变量 / 接入条件 |
|---|---|---|
| 网页手动 | `am song configure --provider manual` | 无需 API Key；输出歌词和风格后等待用户提供音频 |
| Mureka | `am song configure --provider mureka` | `MUREKA_API_KEY`；默认 `mureka-9.5`，API 额度与网页会员分开 |
| Google Lyria | `am song configure --provider lyria` | `GEMINI_API_KEY`；默认 `lyria-3.5`，需对应 Gemini API 权限及计费 |
| 百炼 | `am song configure --provider bailian --workspace YOUR_WORKSPACE_ID` | `DASHSCOPE_API_KEY`；北京地域普通按量 Key、Fun-Music 邀测；当前 Token Plan 不覆盖 |
| ElevenLabs | `am song configure --provider elevenlabs` | `ELEVENLABS_API_KEY`；Music API 权限及付费额度 |
| fal | `am song configure --provider fal` | `FAL_KEY`；托管 `fal-ai/ace-step`，无需部署 |
| MiniMax | `am song configure --provider minimax` | `mmx` 登录；部分账户音乐 API 返回 410，不再向新用户开放 |
| 自建 ACE-Step | `am song configure --provider acestep --endpoint https://YOUR_SERVER` | `ACESTEP_API_KEY`（按服务要求）；需已有服务 |

密钥只在本机终端/密钥管理器设置，不写入 YAML 或项目 `.env`。`am run` 嵌套调用
还需显式追加对应变量名，例如：

```bash
export AM_PASSTHROUGH_ENV="${AM_PASSTHROUGH_ENV:+${AM_PASSTHROUGH_ENV},}FAL_KEY"
```

`doctor` 对托管平台仅检查密钥存在性，不代表权限或额度验证成功。
百炼固定歌词模式会忽略曲风提示，不发送时长/BPM 控制参数；ElevenLabs 使用固定歌词的
composition plan；fal 使用独立队列协议，不能填入自建 ACE-Step 地址。
Mureka 每次请求显式生成一首，避免服务默认两首造成额外费用；歌词最多 5000 字符，
曲风加生成要求最多 1024 字符。Lyria 使用带原歌词的 Interactions 请求，接收完整内联
MP3 音轨；同步请求中断不自动重发。两者的目标时长和 BPM 均为创作提示，
模型返回的歌词或时间标注不会替换项目原歌词或已审核时间轴。

相同配置、歌词和 `--batch` 复用候选。ACE-Step / fal / Mureka 已取得任务 ID 时重复原命令
继续轮询和下载，不重新提交；无任务 ID 的不明请求停止，人工检查后才用新
`--batch` 创建新请求。不自动换提供方。`--timeout` 默认 30 分钟。

对齐工具在仓库外的 Python 3.11–3.13 环境安装：

```bash
python3 -m venv "$HOME/.venvs/am-song"
"$HOME/.venvs/am-song/bin/pip" install \
  'xingyu-lyrics-aligner[alignment] @ git+https://github.com/wangjiqing/xingyu-lyrics-aligner.git@e647b2f3480a9f46a027352713c8e2072d88450d' \
  'librosa==0.11.0'
# 按 Xingyu 官方说明单独准备中文模型；am init 不安装环境或下载权重。
```

将 `song.yaml` 的 `python` 设为该环境 Python 的绝对路径。固定 Xingyu v0.6.1；
librosa 缺失或分析失败会明确降级为歌词驱动，不制造节拍。原始对齐输出、
问题清单及行映射保存在 `production/song/alignment/`；估算字词不会用于逐字同步。
对齐成功不代表唱词正确：人工核对核心知识和术语，抽查句首误差目标 ≤200 ms。
通过 `--timeline FILE --reviewed` 导入修正版前仍会校验摘要、歌词顺序及所有时间。

统一时间轴及其 SRT/短语数据位于 `production/song/`。重复副歌具有独立 ID，
无歌词区段单独记录。镜头从零覆盖完整音频，总帧数向上取整；合成仅在末尾补
不足一帧的静音，不变速、不额外叠 BGM。选歌、音频、歌词或时间轴变化会拒绝
旧镜头依赖；重跑 `song cues` 后使用 `scene run-all --force` 重渲染。

普通测试使用模拟服务，不产生付费调用。真实服务样片需独立验收，不能以
自动化测试代替歌曲内容、对齐误差或视觉质量验收。
