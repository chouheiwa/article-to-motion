# 完成一次从稿件到成片的横屏 MG 视频制作

在当前工作目录完成一次独立的视频制作：把文章、口播稿或参考字幕整理成适合配音的终稿，生成并校对 TTS，依据真实语音重建字幕和时间轴，按语义拆分镜头，并发调度渲染工具制作 MG 动画，再加入首帧封面、背景音乐和音效，最终交付 `final.mp4` 及完整制作证据。

## 一次性项目边界

- 当前目录只服务于本篇内容。每篇新内容都应使用新的干净目录或基于 `main` 新建的独立 worktree。
- 不得复用其他项目的镜头目录、生成音频、素材、日志、检查表、审批文件或 `final.mp4`；即使文件名相同，也必须视为不同项目。
- 开始前记录当前 Git 分支、提交号和工作区状态。不得修改或删除已有的用户文件。
- 如果当前目录已经存在本次流程生成的 `scenes/`、`production/` 或 `final.mp4`，先确认它们是否属于当前项目。无法确认时停止，不得覆盖。
- 为本次项目创建 `production/`，保存配置、输入快照、TTS 证据、音频素材账本、检查报告和审批记录。
- 对所有原始输入生成 SHA-256，并写入 `production/input-manifest.json`。后续不得静默替换输入。

## 工作边界

- 渲染工具负责单个镜头内部的创意、动画方案、代码实现和 MP4 渲染。
- 你负责整理稿件、建立真实时间轴、拆分镜头、准备镜头目录、调整每个镜头 `prompt.md` 的提示词、并发调度、检查产物、处理配音和声音、拼接与最终验收。
- 不替渲染工具设计镜头内部的具体 MG 动效。可以根据镜头文案强化提示词中的语义、艺术方向和表达重点。
- 不得改变 `am` 保证的执行契约：非交互式运行、镜头编号、时长、输出文件名、完整字幕路径、阶段性汇报格式、日志过滤方式和 MP4 交付要求必须保持确定。
- 用 `am scene run-all scenes/` 并发调度镜头，并发数由配置 `SCENE_JOBS` 决定（默认 3）。镜头之间无创作依赖，跨镜头视觉一致性由 `frame.md` 保证。
- 不把任何 API 密钥写入脚本、Markdown、JSON、命令行参数或日志。
- 付费 TTS、云端服务和每个镜头的一次完整渲染调用都有花费和耗时。开始批量配音和批量渲染前，先把预计调用次数（配音段数与试听次数、镜头数乘以可能的重试次数）和所用账户告诉用户并取得同意；同意的范围写进 `production/production-config.json` 的 `spendApproval`，只记账户名称、服务和调用次数上限，不记任何凭据。超出已同意的范围时停下来重新确认。

### 渲染器配置

- 开始前读取仓库根目录的 `article-to-motion.conf`，并考虑当前环境变量或 `.env` 的覆盖值，确定本次使用的 `RENDERER`。
- 把解析后的渲染器写入 `production/production-config.json` 和执行日志。每个镜头用 `am scene run <镜头目录>` 调用，渲染器由 `am` 从配置解析；需要单独指定时写入该镜头 `scene.json` 的 `renderer` 字段，不能静默回退到其他渲染工具。
- 如果配置的渲染工具不可用，停止并明确报告缺失的 CLI；未经用户确认不得替换渲染器。

## 第一阶段：盘点输入和制作规格

先检查当前目录中的文章、Markdown、TXT、参考 SRT、已有音频、图片、视频和视觉参考，生成 `production/input-inventory.md`。

至少确认并记录以下内容到 `production/production-config.json`：

- 项目标识和输入文件。
- 发布平台与目标画幅；未另行指定时沿用项目的 1920×1080 横屏规格。
- 帧率；未另行指定时使用 30fps。
- 稿件是终稿还是仍可编辑。
- SRT 是真实录音时间轴还是仅供参考。
- 是否已有配音；没有配音时使用 TTS。
- 目标音色、语言、语速和情绪；没有确定音色时进入试听关卡，不自行永久决定。
- 是否需要新增开场。
- 第 0 帧封面标题及行数；没有确定时先提供候选并等待确认。
- BGM 与音效的声音方向。

同时冻结本片的内容边界：

- 明确受众、核心问题、必须回答的结论、明确不需要的内容和叙述视角。
- 标题必须直接说明视频主题，不能只追求悬念而丢失主题关键词。
- 用户要求删除个人经历、案例、自我介绍或特定论述时，将其写入禁止项；后续稿件、字幕和画面都不得重新引入。
- 区分“已核实事实”“原作者观点”“视觉隐喻”和“待核实信息”。渲染工具不得自行补充年份、奖项、研究结论、产品能力或其他事实性标签；确需新增时，先补来源证据并等待确认。

缺少文章、口播稿、参考字幕或已有配音中的全部内容时，停止并明确列出缺失输入。

### 第一阶段补充：定风格（仅自定义风格）

根目录 `frame.md` 的 `style_id` 为 `custom-draft` 时，项目用的是按文字描述定制的风格，必须先完成本节，再进入第二阶段；`style_id` 是其他值时跳过本节。风格未定稿时 `am scene run` 会直接拒绝渲染，所以这一步不能推迟。

1. 读 `docs/style-brief.md`，把描述落成具体决定：品牌人格、适用题材、配色、字体角色、四种镜头骨架的版式、动效语法与禁用项。描述含糊时按最贴近原话的解释取舍，并把取舍写进 `production/style-decisions.md`；描述与本片内容明显冲突时停下来确认。
2. 氛围参照图（可选）：若编排工具提供内置生图能力（例如 Codex 的 `$imagegen`），按描述生成一到两张氛围参照图，保存进 `assets/style-guide/mood/`——生成结果默认落在工具自己的数据目录，必须移进项目，否则后续镜头目录里解析不到。工具不提供生图能力、或调用提示该工具不可用时，直接跳过，不得为此安装别的生图工具或改用需要 API 密钥的方式。参照图只用于提炼质感、光影与装饰语言，配色要转成 `colors` 里的十六进制值，图里的文字与字形不得当成字体依据。
3. 选字体：用 `am style fonts` 查看可用字体，用 `am style add-font <字体族> [--weight N]` 拷进项目。只能用这份清单里的字体，或用户已放进 `assets/fonts/` 并附带许可证的 woff2 文件；不得引用本机系统字体，也不得临时从网上下载字体。中文正文必须有中文字体覆盖。
4. 改写 `frame.md` 的 frontmatter：
   - `style_id` 改为 `custom-<小写英文短名>`（只用小写字母、数字和单个连字符），`style_name` 写这套风格的中文名，`scope` 写适用题材。
   - `canvas` 与 `safe_area` 来自画幅预设，一个数字都不许改。
   - `colors` 全部写六位十六进制色；`typography` 的字体栈只能使用 `typography.font_files` 声明过的字体族或通用族名，声明的每个字体都要被某个字体栈用到。
   - `scene_archetypes` 的四个 `id` 与顺序固定，只改 `name` 与 `use_for`；`example_png` 保持 `assets/style-guide/examples/<id>.png`。
   - 有氛围参照图时加上 `mood_refs`，列出它们的相对路径；没有就不写这个键。
   - `motion`、`subtitles`、`cover`、`forbidden` 按风格调整，但 `cover.frame_zero_complete` 必须为 `true`、`cover.stable_frames` 不少于 18，`audio` 的采样率与声道不变。
5. 用同一份 frontmatter 改写 `docs/自定义-视频风格说明书.md`，两份 frontmatter 必须逐项相同；说明书正文写清风格定位、配色用途、字体角色、四种骨架的版式要点、动效、字幕与封面、声音和验收清单。`frame.md` 正文改写成正式的使用说明，删掉草稿说明。
6. 为四种骨架各画一张 1920×1080 的 SVG，存为 `assets/style-guide/examples/<id>.svg`。只用本风格声明的颜色与字体族（`font-family` 写族名），文字用本片真实的标题与要点，不放占位文字。示例图是所有镜头的版式基准，要体现骨架差异而不是同一张图换字。
7. 运行 `am validate style --regenerate-examples` 渲染 PNG 并校验，逐条修正直到输出「风格规范校验通过」。打开 `assets/style-guide/examples/contact-sheet.png` 看一遍：字体是否渲染成声明的字体、配色与描述是否一致、四张骨架是否可区分；不满意就回到第 4 或第 6 步。
8. 把最终的 `frame.md`、说明书、示例图与氛围参照图路径记入 `production/production-config.json`，并在执行日志里列出定稿的 `style_id`。定稿后本片内不得再改风格；要改就重新执行本节并重渲染全部镜头。

### 发布配置生命周期

平台、终稿和封面方向冻结后，立即参考 `templates/publish.md` 在根目录生成首版 `publish.md`，状态通常为 `draft`。文档必须以安全 YAML frontmatter 提供机器字段，并以固定 Markdown 章节提供人工可复制内容；不得把通用模板或其他作品的发布文案直接当成项目结果。

如果将替换已有 `final.mp4`，必须先保留旧视频哈希并把 `video.replacement_in_progress: true`，以 `video_replacement_in_progress` 将现有发布配置原子刷新为 `blocked`。该保护状态优先于封面未确认或人工检查待完成。

## 第二阶段：整理口播稿和开场

根据原文整理适合朗读的 `production/script-final.md`：

- 保留原文事实、观点、因果关系和结论，不擅自增加未经证实的信息。
- 将书面表达调整为自然口语，控制单句长度。
- 专有名词、模型名、产品名、英文词组和强语义短语必须作为完整语义单位，不能在不自然的位置断开。
- 标点服务于朗读停顿，不能只按文字长度机械切句。
- 信息密集段落之间预留呼吸、画面展示和转场空间。
- 如果原稿缺少“这条视频要讲什么”的开场，补充约 12–15 秒的开场：先提出问题或冲突，再说明本视频将完成的实验、演示或验证。
- 开场不能重复正文，也不能提前泄露全部结论。

同时生成 `production/script-change-log.md`，说明删改、重排和新增开场的位置。涉及观点改变或新增事实时必须等待确认；仅做口语化、标点和明显断句修复时可以继续。

### 稿件审核关卡

在进入正式 TTS 前，汇报：

- 预计朗读时长。
- 新增开场全文。
- 与原稿相比的主要变化。
- 需要特别保护的专有名词和不可拆分短语。

稿件未确认时，不生成整条正式配音。

项目根存在 `cast.yaml` 时，本次是多角色叙事项目，必须同时遵守 `PROMPT-CAST-ADDENDUM.md`，其中的分段 TTS 与时间线装配规程覆盖本文件第三、四阶段的对应条款。

### 整片处理方案

稿件确认后、分镜之前，写 `production/treatment.md`，把整片当一件作品来设计，而不是一串各自成立的镜头：

- 主旨：一句话说清看完这条视频的人应该记住什么。
- 钩子—揭示—收束：开头用什么抓住人；中段在哪里翻转、在哪里拿出证据；结尾停在什么画面、什么结论上。
- 贯穿全片的隐喻与持续物件：哪些对象跨镜头保持同一身份，例如同一张结构图逐步点亮、同一个计数器不断累加。每个持续物件记下首次出现的镜头、在哪些镜头发生什么状态变化、在哪个镜头退场。
- 视觉角色分配原则：哪类内容用语境、证据、机制或节奏画面承担（定义见第五阶段「视觉负载与节奏预算」）。

配色、字体、安全区和版式骨架仍由 `frame.md` 管，`treatment.md` 不得重新定义它们，只能在其中选用。第五阶段的分镜和每个镜头的 `prompt.md` 都从 `treatment.md` 推出；某个镜头与它冲突时，先改 `treatment.md` 并确认影响到哪些镜头，再改镜头。

## 第三阶段：TTS 和断句校对

TTS 工具由 `article-to-motion.conf`（或环境变量 `TTS_PROVIDER`）决定，用 `am config get TTS_PROVIDER` 读取解析结果。可选：

| 取值 | 服务 | 自带时间戳 | 适合 |
|---|---|---|---|
| `minimax` | MiniMax（`mmx`） | 句级 | 默认，中文自然度与发音控制最完整 |
| `bailian` | 阿里云百炼（`bl`） | 无，需识别 | 已有百炼额度 |
| `edge` | 微软 Edge 在线语音（`edge-tts`） | 句级，偏粗 | 免鉴权的预览与草稿 |
| `elevenlabs` | ElevenLabs（官方 CLI） | 字级 | 中英混排、需要字级时间戳 |
| `tencent` | 腾讯云语音合成（`tccli`） | 有 | 已有腾讯云账号 |
| `mlx` | 本机 MLX（`mlx-audio`，仅 Apple Silicon） | 无 | 离线、不产生费用 |

各路径的凭据、命令和时间戳获取方式不同，但断句检查、审核关卡和产物要求完全一致。无论哪条路径，第三阶段结束时都必须得到 `production/audio/voice.wav` 与毫秒精度的 `production/audio/voice.srt`；自带时间戳为「无」的路径，`voice.srt` 由第四阶段开头的强制对齐生成。

### 凭据与配置

**MiniMax（`TTS_PROVIDER=minimax`）**

- 配音通过 `mmx` CLI（MiniMax Token Plan）生成，不再直接调用原始 HTTP API。
- 先确认已登录：`mmx auth status`；未登录时用 `mmx auth login --api-key sk-xxxxx` 完成鉴权。
- 凭据保存在用户目录 `~/.mmx/config.json`，不进入项目目录，也不写入脚本、Markdown、JSON、命令行参数或日志。
- 密钥（sk-xxxxx）只存在于登录命令与 `~/.mmx/config.json`，不得写入项目 `.env`、脚本、Markdown、JSON、命令行参数或日志；日志只能记录是否已登录、额度等状态，不得打印密钥值或包含密钥的 URL。
- 若登录后调用报 401，用 `mmx config set --key region --value cn`（国内版）或 `global`（海外版）手动指定服务区域，再用 `mmx auth status` 确认。
- 用 `mmx quota` 查看 Token Plan 剩余额度。

**百炼（`TTS_PROVIDER=bailian`）**

- 配音通过 `bl` CLI（阿里云百炼）生成，使用 CosyVoice 模型。
- 先确认已登录：`bl auth status --output json`；未登录时用 `bl auth login --console`（浏览器授权）或 `bl auth login --api-key <key>` 完成鉴权。
- 凭据保存在用户目录 `~/.bailian/config.json`，不进入项目目录，也不写入脚本、Markdown、JSON、命令行参数或日志。
- 密钥只存在于登录命令与 `~/.bailian/config.json`，不得写入项目 `.env`、脚本、Markdown、JSON、命令行参数或日志；日志只能记录是否已登录、额度等状态，不得打印密钥值或包含密钥的 URL。
- 用 `bl usage free` 或 `bl usage stats` 查看剩余额度。

**Edge 在线语音（`TTS_PROVIDER=edge`）**

- 用 `pipx install edge-tts`（或 `uv tool install edge-tts`）安装，免鉴权。
- 它调用的是微软 Edge 浏览器的朗读接口，不是有服务承诺的商用 API，随时可能失效或限流。只用于预览和草稿；正式交付前确认用户接受这一点，否则换其他路径。

**ElevenLabs（`TTS_PROVIDER=elevenlabs`）**

- 用 `brew install elevenlabs/tap/elevenlabs` 或 `npm install -g @elevenlabs/cli` 安装官方 CLI。
- 先用 `elevenlabs auth login` 登录，凭据存进系统钥匙串，不进入项目目录。不得在项目 `.env` 写 `ELEVENLABS_API_KEY`：这个 CLI 会自动加载当前目录的 `.env`，`am` 也会拒绝在 `.env` 里出现密钥的项目。
- 密钥不得写入脚本、Markdown、JSON、命令行参数或日志。

**腾讯云（`TTS_PROVIDER=tencent`）**

- 用 `pip install tccli` 或 `brew tap tencentcloud/tccli && brew install tccli` 安装官方 CLI。
- 用 `tccli configure` 交互配置凭据，凭据保存在用户目录，不进入项目目录；不得把 SecretId / SecretKey 写进项目文件、命令行参数或日志。

**本机 MLX（`TTS_PROVIDER=mlx`）**

- 仅限 Apple Silicon。用 `uv tool install --prerelease=allow mlx-audio` 安装，免鉴权；首次运行会下载模型到用户缓存目录。

### 试听和正式生成

- 音色尚未确定时，先列出可用音色，选择中文（普通话）真诚成年男声对应的 voice ID，记录到 `production/production-config.json` 的 `ttsVoiceId`。
  - MiniMax：`mmx speech voices`
  - 百炼：`bl speech synthesize --list-voices --model cosyvoice-v3-flash`
  - Edge：`edge-tts --list-voices | grep zh-CN`
  - ElevenLabs：`elevenlabs voices search --format json`
  - 腾讯云：CLI 没有列表命令，按官方「音色列表」文档选择 `VoiceType`
  - MLX：Qwen3-TTS 预置中文音色 `Vivian`、`Serena`、`Uncle_Fu`、`Dylan`（北京话）、`Eric`（四川话）
- 用同一段 10–20 秒代表性文案生成 2–3 个试听版本（`--out production/audio/try-<voiceId>.wav`），文件名包含 voice ID。
- 音色、语速确认后再生成正式配音。
- 优先按完整语义段落调用 TTS，避免按字幕行逐条生成造成语调碎裂。
- 正式生成前，先扫描终稿全文，把容易误读的英文单词、英文缩写、产品名、模型名、专有名词和数字逐一列出并确定预期读音，再写入对应 TTS 的发音控制；同一词出现多次也要统一。不要靠生成后试听再补。
- 四位数年份一律逐位读：`1972 年` 读「一九七二年」，`2025 年` 读「二零二五年」，`2026 年 2 月` 读「二零二六年二月」，`1970 年代` 读「一九七零年代」；不得读成「一千九百七十二年」「两千零二十五年」。扫描时把全文每一个四位年份都列入发音控制，不要交给 TTS 自行判断：同一音色对不同年份、不同上下文的处理并不一致。其余数量数字（`16 个`、`815 字`、`64KB`）仍按数值读。
- 把实际送进 TTS 的完整文本（已做年份、读音等改写）保存为 `production/audio/tts-text.txt`，第四阶段强制对齐要用它。
- 保留原始请求命令（去掉密钥）、CLI 返回的元数据（时长、采样率、大小）、原始音频和时间戳文件，写入生成报告。
- 不得手工伪造服务返回的时间戳。

**MiniMax 固定参数**

- 模型使用 `--model speech-2.8-hd`，音量默认 `--volume 1`、音高默认 `--pitch 0`。语速不得被模板永久硬编码为已确认值：可从 `0.98` 开始试听，但必须让用户根据代表性密集段落和完整试听确认，最终值写入项目配置。情绪由音色本身表达（`mmx` 不提供 `emotion` 参数），选择偏平静、真诚的音色，不通过变调模拟。
- 需要双声道 PCM 时，加 `--format wav --channels 2` 输出，便于后续统一混音。MiniMax WAV 采样率仅支持 `8000/16000/22050/24000/32000/44100`，不支持 48000；需要 48kHz 时用 `--sample-rate 44100`，或改用 MP3 输出。
- 用 `--subtitles` 同时取得真实句级时间戳，例如：
  `mmx speech synthesize --text "..." --voice <voiceId> --speed <confirmedSpeed> --subtitles --out production/audio/voice.wav`
  该命令会额外产出 `production/audio/voice.srt`（毫秒精度），作为第四阶段重建字幕的真实依据。
- 用 `--pronunciation 原文/替换内容` 钉住上面的易误读词（可重复传入多条）。替换内容按词的语言和意图选择记音法：
  - 英文词用括号包裹的 IPA 音标，例如 `--pronunciation "MiniMax/ˈmɪniˌmæks"`、`--pronunciation "AI/ˌeɪˈaɪ"`。
  - 中文多音字用括号包裹的拼音带声调，例如 `--pronunciation "处理/(chu3)(li3)"`。
  - 缩写按字母念或作文本展开时用纯文本，例如 `--pronunciation "API/诶屁爱"`、`--pronunciation "omg/oh my god"`；仍要按中文读音读的品牌/产品名直接写汉字，例如 `--pronunciation "MiniMax/米尼麦克斯"`。
  - 四位年份连同「年」字一起替换成逐位汉字，例如 `--pronunciation "2025年/二零二五年"`、`--pronunciation "1972年/一九七二年"`；带上「年」是为了不误伤同样四位、但表示数量的数字。原文里数字与「年」之间有空格时，匹配串照原文写。

**百炼固定参数**

- 模型 `--model cosyvoice-v3-flash`（CLI 默认值）、语速 `--rate 0.98`、音量 `--volume 50`、音高 `--pitch 1.0`。情绪通过 `--instruction "平静、真诚的语气"` 控制，选择偏平静、真诚的音色，不通过变调模拟。
- 需要 48kHz WAV 时，加 `--sample-rate 48000 --format wav` 输出，便于后续统一混音。
- 百炼 TTS 不直接产出时间戳，需要两步取得真实句级时间戳：
  1. 合成音频：`bl speech synthesize --text "..." --voice <voiceId> --rate 0.98 --format wav --sample-rate 48000 --out production/audio/voice.wav`
  2. 识别时间戳：`bl speech recognize --url production/audio/voice.wav --language zh --out production/audio/voice-asr.json`
  从 ASR 结果中提取句级开始/结束时间，转换为 `production/audio/voice.srt`（毫秒精度），作为第四阶段重建字幕的真实依据。
- 用 SSML `<phoneme>` 钉住上面的易误读词（需加 `--enable-ssml`）。`alphabet` 必须匹配词的语言：中文词用 `py`（拼音），英文词必须用 `ipa`（国际音标）——用拼音去近似英文只会得到中式英文。
  - 英文词用 `alphabet="ipa"`，例如 `<phoneme alphabet="ipa" ph="ˈmɪniˌmæks">MiniMax</phoneme>`、`<phoneme alphabet="ipa" ph="ˌeɪˈaɪ">AI</phoneme>`。
  - 中文多音字用 `alphabet="py"`，例如 `<phoneme alphabet="py" ph="he2">和</phoneme>`。
- 四位年份：在送进 TTS 的文本里直接写成逐位汉字（`2025 年` 写「二零二五年」），不依赖 SSML 数字读法。字幕仍显示原文的阿拉伯数字：从 ASR 结果重建 `voice.srt` 时，按终稿把这些年份换回原写法，并在生成报告里列出替换清单。

**Edge 固定参数**

- 命令：`edge-tts -v <voiceId> --rate=<±N%> -f production/audio/tts-text.txt --write-media production/audio/voice.mp3 --write-subtitles production/audio/edge.srt`。语速写成百分比，负数必须用等号连写（`--rate=-2%`），否则被当成另一个参数。
- 只输出 24kHz 单声道 MP3，用 FFmpeg 转成 `voice.wav`（48kHz 双声道）供后续混音。
- 不支持自定义 SSML，发音控制只能改写送进 TTS 的文本：年份写成逐位汉字，易误读的英文写成能读对的形式；字幕仍显示终稿原文，替换清单写进生成报告。
- `edge.srt` 是句级时间戳，实测起点比发声早约 0.1–0.2 秒，结尾还把句间停顿算进去。它只能做参考，`voice.srt` 必须由第四阶段的强制对齐生成。

**ElevenLabs 固定参数**

- 用带时间戳的合成命令：`elevenlabs text-to-speech convert_with_timestamps --voice-id <voiceId> --model-id eleven_multilingual_v2 --text "..." --voice-settings.speed <confirmedSpeed> --output-format mp3_44100_128 --format json > production/audio/tts-001.json`。
- 返回 JSON 里同时有 base64 编码的音频和字级对齐（`alignment` 下的 `characters`、`character_start_times_seconds`、`character_end_times_seconds`）。先打印 JSON 的字段名确认结构再解析：解码音频后转成 `voice.wav`，按字级时间合成句级 `voice.srt`。
- 数字读法用 `--apply-text-normalization on` 交给服务处理，但四位年份仍在文本里预先写成逐位汉字。

**腾讯云固定参数**

- 单次请求最多 150 个汉字（全角标点也算一个字），必须按句切段，每段一次请求：`tccli tts TextToVoice --region ap-guangzhou --Text "..." --SessionId <uuid> --VoiceType <voiceId> --Codec wav --SampleRate 16000 --Speed <confirmedSpeed> --EnableSubtitle True > production/audio/tts-NNN.json`。`--Speed` 取值 -2 到 6，0 是正常语速。
- 每段输出里的 `Audio` 是 base64 音频，`Subtitles` 是该段内的时间戳（毫秒）。各段音频按顺序拼接；字幕时间要加上该段在整条配音中的起点，起点用 `ffprobe` 实测前面各段的时长累加，不得按字数估算。拼接后的整条时间戳只能作为参考，`voice.srt` 仍以第四阶段强制对齐为准。

**MLX 固定参数**

- 命令：`mlx_audio.tts.generate --model mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit --text "..." --voice <voiceId> --lang_code Chinese --output_path production/audio --file_prefix voice --join_audio --audio_format wav`。`--lang_code Chinese` 必须显式传，缺省按英文合成；不加 `--join_audio` 时会按文本分段输出多个文件。输出是 24kHz 单声道，用 FFmpeg 转成 48kHz 双声道再混音。
- 不产出时间戳，`voice.srt` 由第四阶段强制对齐生成。
- 发音控制同 Edge，只能改写送进 TTS 的文本。

### 断句检查

重点检查：

- 产品名、模型名或短语是否被错误拆开。
- "主语—谓语""动词—宾语""条件—结论"是否被不自然地切断。
- 数字、英文缩写和专有名词是否读对；英文词应为标准英文发音，不能是拼音近似的中式英文。
- 每个四位年份是否逐位读出（「二零二五年」），没有读成「两千零二十五年」。
- 长句是否有足够呼吸。
- 句尾是否留出画面展示时间。

发现断句或发音问题时，先修改稿件标点、分段或发音控制，再重新生成受影响段落；不要只靠后期剪切掩盖。发音控制必须覆盖生成前扫描列出的全部易误读词，同一词出现多次也要统一。

### 配音审核关卡

正式时间轴冻结前，至少交付：

- 完整配音试听文件。
- 本次音色和参数。
- 断句与发音检查结果。
- 需要人工确认的疑点。

试听必须同时覆盖开场、信息最密集段落、英文或数字最多的段落和结尾。仅听一小段不能证明整篇口播速度合适。语速确认必须早于正式时间轴和镜头渲染。

正式配音一旦变更，旧的 SRT、词级对齐、总帧数、分镜、镜头时长、cue sheet、混音和候选视频全部标记为失效并重新生成；不得把旧画面整体拉伸去追新配音。

## 第四阶段：依据实际语音重建 SRT

### 强制对齐与时间轴校验

文稿是现成的，把它逐字对到配音上比语音识别更准，也不会把专有名词识别错。对齐工具由 `ALIGN_PROVIDER` 决定，用 `am config get ALIGN_PROVIDER` 读取：缺省在 Apple Silicon 上是 `mlx`，其余平台是 `funasr`。

送进对齐的文本必须是实际送进 TTS 的那份（年份已写成逐位汉字等），对齐完再按替换清单映射回终稿原文。先用 FFmpeg 把 `voice.wav` 转成 16kHz 单声道的 `production/audio/voice-16k.wav`。

**`mlx`（Qwen3-ForcedAligner）**

- 安装同 MLX 语音合成（`uv tool install --prerelease=allow mlx-audio`）。
- 命令：`mlx_audio.stt.generate --model mlx-community/Qwen3-ForcedAligner-0.6B-8bit --audio production/audio/voice-16k.wav --text "$(cat production/audio/tts-text.txt)" --language Chinese --output-path production/audio/align --format json`，结果写到 `production/audio/align.json`（`--output-path` 是不带扩展名的文件路径，不是目录）。
- `--language Chinese` 必须显式传，缺省是英文。
- 输出按单字给出开始、结束秒数，粒度约 0.08 秒；标点会被丢掉，按顺序与原文逐字对回。
- 单次最多 5 分钟音频。更长的配音在句间停顿处切段分别对齐，再按各段起点平移；切点从下面 `am audio check` 报告的 `speech` 区间之间选，不要切在发声中间。

**`funasr`（时间戳预测）**

- 安装：`pip install funasr torch torchaudio`。没有命令行入口，用 Python 调用：

  ```python
  from funasr import AutoModel
  model = AutoModel(model="iic/speech_timestamp_prediction-v1-16k-offline")
  result = model.generate(input=("production/audio/voice-16k.wav", "实际送进 TTS 的文本"), data_type=("sound", "text"))
  print(result)
  ```

- 先打印结果确认结构，再按逐字时间戳合成句级 SRT。

对齐结果保存为 `production/audio/align.json`（分段对齐时用 `align-NN.json`），并据此生成（或替换）`production/audio/voice.srt`。

**每次得到或改动 `voice.srt` 后，都要用配音本身做一次独立校验：**

```bash
am audio check --audio production/audio/voice.wav --srt production/audio/voice.srt \
  --report-json production/audio/voice-check.json
```

它不识别文字，只用能量包络判定每一刻有没有声音，所以与时间戳来自哪里无关。判断结果读 `voice-check.json`：

- `problems` 里是字幕早于或晚于发声、整段落在静音里、发声没有字幕覆盖等问题，必须修掉再继续。优先换用强制对齐或修正对齐输入，不得手工挪时间码凑数。
- `hints` 里的「整体偏移」说明时间戳来源有系统误差，要整体校正；「结尾停留」对 TTS 或对齐直接产出的 `voice.srt` 说明它偏晚。
- `transcription-production.srt` 生成后同样跑一次。终稿字幕按可读单元延长停留是允许的，所以它的「结尾停留」提示可以保留，但 `problems` 必须为空。

- 参考 SRT 只用于理解原始内容和大致节奏。没有已录配音时，不把参考时间码当作最终时间码。
- 以 `production/audio/voice.srt` 的真实时间戳生成 `transcription-production.srt`。MiniMax 由 `--subtitles` 直接产出，百炼由 ASR 识别后转换产出，ElevenLabs 由字级对齐合成；Edge、腾讯云与 MLX 由下面的强制对齐生成。该 SRT 已是毫秒精度的句级时间戳，可直接作为基础，再按可读语义单元微调。
- 字幕按可读语义单元切分，不能直接照搬 TTS 请求分段。
  - 复合词、人名、机构名、修饰语与中心词、数量与单位、英文短语不拆到两条字幕或两行里。
  - 不要连续出现很碎的两三字字幕条。
  - 先按语义定分组和断行，再单独决定哪些词做强调；不为了突出某个词去改断行。
  - 整句说完后仍留在画面上的标题属于画面排版，不属于字幕，不写进 SRT。
- 时间使用毫秒精度，不得四舍五入或截断为整数秒。
- 字幕之间的无文字间隔必须保留，用作呼吸、元素展示、镜头收尾或转场。
- 根据画面信息密度延长节奏：元素越多，进入、停留、退出所需时间越长。不要让字幕刚读完就立即切镜头。
- 最终节目时长必须对齐整数帧。30fps 下，先确定总帧数，再由 `总帧数 ÷ 30` 得到视频时长。
- 配音不足整帧节目终点时，用静音补齐；不得拉伸人声来填充展示时间。
- 节目终点必须由“最后一个有效语音词的结束帧 + 已批准的结尾停留帧数”推导，不能继承旧模板、旧 BGM 淡出点或旧候选时长。
- 在配置中记录 `closingHoldFrames` 和允许的最大尾留白。候选交付前实测最后一个有效语音词到节目终点的帧差；异常长的结尾留白必须先确认，不能默认保留。

生成 `production/timing-report.json`，至少包含：

- 帧率、总帧数和总时长。
- 音频采样率和每声道目标采样数。
- 每条字幕的开始、结束和文本。
- 所有无文字停顿区间。
- 参考 SRT 与新 SRT 的总时长差异。

### 第四阶段补充：短语级语义时间轴

句级 SRT 只能用于字幕和粗排，不能直接证明一句话内部的视觉顺序。凡一条字幕包含两个以上会分别显示的概念、动作、条件、结论或专有名词，必须依据真实音频执行词级或短语级强制对齐（工具同上，直接复用 `production/audio/align*.json` 的逐字结果），并生成：

- `production/phrase-timeline.json`
- `production/visible-event-inventory.json`

两个文件的字段名固定，`am validate reads` 按这些名字读取；帧号一律是全片全局帧。

`phrase-timeline.json`：顶层 `{"schema": 1, "fps": 30, "phrases": [...]}`，每个受保护短语记录：

- `phrase_id`、`subtitle_index`（字幕编号）、`source_text`（原文）、`semantic_order`（语义顺序）。
- `start_ms`、`end_ms`，以及 `start_frame`、`end_frame_exclusive`。
- `alignment_source`（对齐来源）、`confidence`（置信度）、`reviewed`（人工复核状态）。
- `visible_event_ids`（对应的可见事件 ID）。

`visible-event-inventory.json`：顶层 `{"schema": 1, "fps": 30, "events": [...]}`，每个可见事件记录：

- `event_id`、`scene_id`（所属镜头）、`selector`（DOM selector 或稳定元素 ID）、`text`（显示文本）、`role`（语义角色；纯装饰元素写 `decorative`）。
- `phrase_id`（绑定的短语）。
- `enter_frame`、`readable_start_frame`、`readable_end_frame_exclusive`、`exit_end_frame`（退出完成帧）。

必须验证：

- 非装饰性的可读元素不得早于绑定短语开始帧出现。
- 视觉事件顺序必须与真实口播短语顺序一致。
- 一个元素不得包含尚未说出的未来信息。
- “条件—结论”“主语—谓语”“看谁—传什么—最终结论”等不可颠倒结构必须分别绑定。
- 复合标题如果包含多个先后出现的受保护短语，必须拆成独立可控元素，或推迟到整句说完后作为总结出现。
- 每个受保护短语至少生成“开始前一帧、开始帧、稳定可读帧”三联证据。
- 每个非装饰元素的可读区间必须够观众读完：不短于 5/6 秒，也不短于「字数 ÷ 每秒 9 字」（一个汉字、一个英文词或一个数各算一个字）。这是字幕的下限，画面元素还要先被找到，信息越密集留得越长。一次只给观众一个信息点：前一个还没读完，不要让下一个开始。

## 第五阶段：按语义拆分镜头

根据 `transcription-production.srt` 做粗粒度分镜：

- 一个镜头可以包含一条或多条连续字幕。
- 合并依据是同一主题、同一因果关系或同一视觉概念，不按固定字幕条数机械切分。
- 每个镜头对应一个连续时间区间。
- 所有镜头按顺序首尾相接，完整覆盖从节目第 0 帧到最终帧的总跨度。
- 无文字空白并入前一个镜头作为停顿、收尾或转场；很长的空白可以单独成为静默/转场镜头。
- 镜头时长以秒表示并保留毫秒精度，例如 `2.833`、`6.500`。

调用渲染工具前必须复核：

- 所有镜头 `scene.json` 中 `duration_seconds` 之和等于最终节目时长，误差不超过 0.1 秒。
- 换算后的各镜头帧数之和等于总帧数。
- 每个镜头先定帧数，再写 `duration_seconds = 帧数 ÷ 帧率`，保留 6 位小数（431 帧写 `14.366667`，不写 `14.367`）。渲染器按向上取整出帧，14.367 秒是 431.01 帧，会渲出 432 帧；`am scene run-all` 在渲染前会拒绝任何非整数帧的时长。
- 没有时间重叠、缺口或负时长。

把分镜写入 `production/scene-plan.json` 和 `production/scene-plan.md`。

### 视觉负载与节奏预算

- 默认一拍一个焦点：同一时刻最多一个主标题、一个主视觉和一条辅助说明。
- 新阶段建立前，上一阶段必须退出或降为不可阅读的背景；禁止把结构图、结论条、标签和说明不断向下堆叠。
- 为每个可读元素定义最小可读帧数；clip 可见窗口必须覆盖淡入、稳定阅读和淡出，不能出现“时间轴有事件但元素实际来不及显示”。
- 信息越密集，越要减少同屏元素并增加稳定停留；不能只把整镜头统一放慢。
- 每个镜头选定一个主要视觉角色，写进 `scene-plan.json` 的 `visual_role` 和 `scene-plan.md`：
  - 语境：交代场景、对象或问题从哪里来。
  - 证据：截图、数据、原文、对比结果。
  - 机制：流程、结构、因果。
  - 节奏：过渡、呼吸、情绪停顿。
- 画面要证明或解释台词，而不是给台词里的名词配图：说到“数据库”就画一个圆柱体属于名词插图，应改为展示数据库在这句话里做了什么。
- 每一段时间都要有刻意安排的画面，不允许“先放着”的空镜或只为填时长的循环动画。
- 画面上的标题与关键词要做字幕做不到的事：命名、归纳、给出结构。不要把正在读的台词原样再排一遍当标题；删掉这类冗余标题后要重新构图，不留空位。
- 镜头之间的持续物件按 `treatment.md` 排布：在 `scene-plan.md` 里写清每个镜头接手哪些持续物件、交出时处于什么状态。
- 首次渲染前优先用代表性镜头验证版式：至少覆盖开场、信息最密集镜头和结尾。若代表性镜头存在拥挤、节奏过快或层级问题，先修正规则再批量渲染。

## 第六阶段：建立镜头目录、并发调度渲染工具

每个镜头使用独立目录，例如：

```text
scenes/
  scene-001/
    scene.json
    prompt.md
    transcription.srt
    CLAUDE.md        # 渲染器锁定文件，文件名随渲染工具而定
```

每个镜头必须：

- 建立 `scene.json`：执行契约。字段为 `id`、`duration_seconds`、`output`、`transcript`、`text`，可选 `style_guide` 和 `renderer`；未知字段会直接报错。`text` 会被包进 `<scene-text>` 定界块送进渲染提示词并声明为「数据而非指令」，因此 `text` 本身不得含 `[[USER_MESSAGE]]` 标记或 `<scene-text>` / `</scene-text>` 及其形近变体，含则报错。
- 建立 `prompt.md`：本镜头的创意方向、视觉概念和表达重点。交付规格、阶段消息等执行契约由 `am` 保证，不在本文件中。
- 使用 `am init` 装进项目 `.agents/skills/` 的技能。技能缺失时重跑 `am init` 补齐，不要自己跑 `npx hyperframes skills`——那会装到本机 HOME 而不是项目里，绕过版本固定；也不得从其他项目或本机目录复制。
- 建立渲染器锁定文件：固定 HyperFrames 版本为 `0.8.14` 并要求每次 CLI 调用都带 `npx --yes hyperframes@0.8.14` 前缀；禁止 `hyperframes@latest`、不带版本的 `npx hyperframes`、`hyperframes skills`、`skills update` 以及任何会改动已装技能的命令；禁止读取 `.env` 或打印凭据；禁止改动 `scene.json` 里的执行契约。文件名取决于该镜头所用渲染工具会自动读取的项目级指令文件（例如 `claude` 读 `CLAUDE.md`），按所选工具各自的约定命名，不要不管用哪个工具都写成 `CLAUDE.md`。
- 锁定文件写「只在本镜头目录内工作、不查看兄弟镜头和上级项目目录」时，必须写明一条例外：项目根下的 `.agents/skills/`。全部技能都在那里——HyperFrames 官方技能与随 `am` 二进制下发的 `text-to-lottie`、`algorithmic-art` 同处一个目录，路径由 `am` 解析后注入提示词。它在项目内但在镜头目录之外，会被「只在本镜头目录内工作」挡住；缺这条例外，渲染工具会拒读动效 rule 索引，动画退化成只有淡入和位移。
- 把完整的 `transcription-production.srt` 复制为镜头目录中的 `transcription.srt`，让渲染工具理解全文上下文。
- 仓库根目录存在视觉规范文件（如 `frame.md`）时必须复制进镜头目录，并在 `scene.json` 的 `style_guide` 中声明。声明后，画布规格、配色、字体层级和安全区会作为强制约束写进渲染提示词，这是跨镜头视觉一致性的唯一保障；声明了但文件不存在会报错。
- 把视觉规范 `scene_archetypes` 各项 `example_png` 指向的示例图按**相同的相对路径**复制进镜头目录（默认即 `assets/style-guide/examples/`）。渲染工具在设计版式和画面自查时要对照它们；不复制的话，镜头目录里解析不到这些路径，渲染工具从未见过任何视觉参照。
- 视觉规范声明了 `mood_refs` 时，把其中的氛围参照图同样按**相同的相对路径**复制进镜头目录。渲染工具只从中取质感与装饰语言，配色、字体与版式仍以视觉规范的数值为准。
- 把视觉规范 `typography.font_files` 声明的字体文件按**相同的相对路径**复制进镜头目录（默认即 `assets/fonts/`），使规范里的路径在镜头目录内同样成立。渲染机是一台干净的无头 Chrome，不装任何系统字体：字体栈里没有 `@font-face` 的字体族会静默回退，本地渲染看着正常、成片排版却是错的，且不报错。中文字形只能由这些自带文件提供，不得改用 `PingFang SC`、`Hiragino Sans GB` 这类只存在于本机的系统字体。
- 在 `prompt.md` 中写入创意描述，使艺术方向和视觉概念贴合当前文案，但不能改动 `am` 保证的执行契约。创意描述从 `treatment.md` 和本镜头在 `scene-plan` 中的视觉角色推出，写明画面要证明或解释的是哪句台词。
- 在 `prompt.md` 中写明镜头交接：入场状态（承接上一镜头的哪个持续物件、它在第 0 帧的位置、大小与状态）与出场状态（最后一帧留给下一镜头的物件、位置与状态）。各镜头分别渲染、互相看不到，交接只能靠这两段约定；相邻两个镜头的出场状态与入场状态必须逐项对得上。第一个镜头没有入场状态，最后一个镜头的出场状态就是全片收束画面。
- 在 `prompt.md` 中提醒画面文字与字幕分工：标题与关键词只做命名、归纳和给结构，不复述正在读的台词。
- 要求输出 1920×1080、30fps、静音、无音轨的 MP4。
- 在 `prompt.md` 中附上该镜头的全局起止帧、短语事件表、禁止提前出现的未来短语、最小可读帧数和结尾停留要求；这些内容作为创意实现的时间合同，不改变 `am` 已有执行字段。
- 要求渲染工具先盘点所有可读 DOM 元素，并确保它们全部登记到 `visible-event-inventory.json`；装饰元素可豁免，但必须明确标记为装饰。

阶段性汇报和日志规则必须保留：

- 只向外放行以 `[[USER_MESSAGE]]` 开头的消息。
- 保留 `render-<scene>.stream.jsonl`。
- 保留 `render-<scene>.stderr.log`。
- 保留 `render-<scene>.user.log`。
- 阶段消息至少覆盖需求与素材检查、联网搜索、代码完成开始渲染、MP4 渲染完成。

用 `am scene run-all scenes/ --srt transcription.srt --strict-coverage --report-json production/run-report.json` 一次性调度。已有合格产物的镜头会被跳过；输入比产物新的镜头会被跳过并警告，需要重渲染时用 `am scene run <镜头目录>`。渲染器非零退出自动重试（退避 10 秒与 30 秒）并把失败证据保留在 `attempts/attempt-NN/`；产物校验失败不重试。

`--srt` 加 `--strict-coverage` 在开始渲染前核对镜头编号唯一性与「镜头总时长等于字幕跨度」。务必带上：这条规则本来就写在第五阶段，但漏掉一镜等到拼接时才发现的话，每一镜都已经烧掉一次完整的渲染调用。

产物校验查画幅、帧率、时长和「必须无音轨」四项，画幅取自 `frame.md`。不符时回镜头工程重渲染，不要在后面的阶段缩放补救。

产物校验还会静态扫描镜头合成源码：读时钟、`Math.random()`、`requestAnimationFrame`、`setInterval`、无限 `repeat`、渲染时联网、在线字体与未固定版本的 CDN 库都会让同一帧在两次渲染间不同，命中即判失败且不重试。改完合成代码、重渲染前可用 `am scene lint <镜头目录>` 先确认。

`--report-json` 落盘的报告是判断结果的依据，不要去解析终端上的中文汇总。每个镜头一条记录，字段为 `scene_id`、`status`（`succeeded` / `skipped` / `stale` / `failed`）、`attempts`、`seconds`、`reason`；顶层还有 `counts`、`exit_code` 和 `interrupted`。被中断时报告照常落盘，据 `interrupted` 与各镜头 `status` 判断哪些还需要重跑。

渲染前语义关卡：

- 镜头源码中的可读文字顺序必须与 `phrase-timeline.json` 一致，不能让旧 GSAP 时间点、旧模板阶段或创意标题反过来决定词序。
- 关键文字使用由绝对短语帧换算得到的源级时间；阶段时间适配器只可用于装饰和非语义动画。
- 禁止用 FFmpeg `setpts`、整体变速、抽帧或复制帧修复文字—配音同步。需要调整语义时间时，修改源工程时间轴并只重渲染受影响镜头。
- 修订已有镜头时，先建立可复现的失败证据，再做最小源级修改；不得一边改布局、一边改配音、一边改全片节奏。

如果一段时间没有新的 `[[USER_MESSAGE]]`，不要立即判定失败。先检查：

- 流日志和错误日志是否仍在写入。
- 项目文件、渲染目录和 MP4 是否有更新时间。
- 是否存在 HyperFrames、FFmpeg、Chromium 或 Node 渲染进程。
- 脚本最终退出码。

只有在脚本退出失败，或长时间无日志、无文件更新且无渲染进程时，才记录该镜头失败。重试必须使用新的 attempt 目录或保留旧日志，不能覆盖失败证据。

## 第七阶段：首帧封面和视觉验收

用于抖音等竖屏平台时，第 0 帧必须是可直接作为封面的完整画面：

- 使用已确认的封面标题，严格保持文字、行数和标点。
- 第 0 帧不能是黑帧、空白帧、半透明过渡态或尚未完成的动画状态。
- 标题位于安全区，无遮挡、无裁切，并与背景保持足够对比度。
- 标题至少稳定若干帧后再开始转场；稳定帧数写入项目配置和视觉检查表。
- 对第 0 帧、转场开始帧和转场进行中的代表帧截图检查。
- 眼镜、连线、图标、人物或其他核心视觉隐喻必须检查几何形状、对齐和语义是否正确。

抽帧用 `am scene frames <镜头目录> --out production/visual-qc/<镜头编号> --report-json production/visual-qc/<镜头编号>.json`，不要自己拼 FFmpeg。`--at` 接受秒数、百分比和 `end`，可混用；`end` 取时长减一帧，直接用时长会超出末帧，FFmpeg 会返回成功却什么都不输出。

静帧看不出动作。入场、阶段切换和转场用 `--strip 开始:结束` 把该段每一帧拼成一张条带图，检查有没有一帧蹦出或闪烁、是否全程匀速、几个元素是否同一帧齐动、上一个信息点读完前下一个是否已经抢视线。小字号文字用 `--crop x,y,宽,高` 在原始分辨率下看清楚，不要凭缩略图判断可读。

第 0 帧要做封面时加 `--at 0 --check-blank`，空白帧直接判失败。判定阈值刻意保守，只拦几乎完全均匀的画面：深色背景配浅色标题是常见设计，偏暗但有内容的帧只进报告的 `hints`，不影响退出码。

命令只负责把 MP4 变成可以逐张打开看的 PNG，并标出纯色帧这种机器能判的情况。画面对不对要你自己看图判断——上面列的几何、对齐和语义检查没有任何机器替代。

镜头代码定稿、`visible-event-inventory.json` 按实际 composition 填好后，用一条命令核对阅读时间、短语绑定和画面顺序：

```bash
am validate reads --report-json production/visual-qc/reads-check.json
```

`problems` 必须为空：不够读的元素回镜头工程延长可读区间或拆开信息点，不得只改清单里的数字。`hints` 里「同时开始可读」的元素要确认它们是同一个信息点（如一组卡片），否则错开出场。

### 画面文字对账

渲染工具只看得到本镜头的字幕，为了填满图表和读数面板时会补出原文里没有的数字。所有镜头定稿后，逐镜头列出画面上出现的每个数字（含百分比、年份、版本号与单位）、型号、产品名、人名和引语，逐条对回终稿原文，写进 `production/visual-qc/onscreen-facts.json`：每条记录镜头编号、画面上的写法和原文出处片段；纯装饰的读数（HUD 刻度、时间码、坐标）标为装饰，不写出处。

- 找不到出处的条目改画面，不改文章，也不在清单里补一个出处。
- 图表的刻度、比例和排序必须与出处数值一致；原文只给了定性描述的，不画带刻度的图表。
- 装饰读数不得带有会被读成事实的标签或单位（如「准确率 98.7%」）。
- 跨镜头重复出现的同一个量，写法和数值必须一致。

把截图、检查结果和修复记录保存在 `production/visual-qc/`。视觉未通过时，只重渲染相关镜头，不重做已冻结的配音和其他镜头。

视觉验收不能只看静态构图，还必须检查：

- 每个受保护短语在实际候选视频中的前一帧、开始帧和稳定帧。
- clip 窗口与 GSAP 淡入、停留、淡出是否真实相交。
- 同一位置替换的文字是否按口播顺序出现，是否残留上一词或提前显示下一词。
- 动画速度是否连续。出现卡顿时先检查是否做过二次时间映射、像素级变速、重复帧或极短 clip。
- 最后一句结束帧、结尾稳定帧和最终帧；结尾不能无依据地保留长时间静态画面。

## 第八阶段：规范化和拼接静音母版

用 `am concat scenes/ --out production/silent-master.mp4` 完成规范化和拼接。先跑一次 `--dry-run` 看有哪些镜头规格不一致，再正式执行。

这条命令按镜头目录名字典序拼接，规格一致时全程 `-c copy` 不重新编码；只有编码或像素格式与目标不同的片段会先生成规范化副本，渲染工具的原始 MP4 保留在原处。拼完会自证时长等于各片段之和、无音轨、画幅与帧率符合目标。

规范化只用于编码、像素格式和封装统一，不承担语义重定时。分辨率或帧率不符时命令会直接失败并要求回镜头工程重渲染——缩放会损失画质，改帧率会动到时间轴。禁止用 `setpts`、整体变速或抽帧复制帧让镜头追赶配音；命令本身不提供这些能力，也不要绕过它自己拼 FFmpeg 去做。

只有一个镜头时同样用这条命令。母版生成后跑：

```bash
am validate video production/silent-master.mp4 --silent --decode \
  --expect-frames <timing-report.json 的总帧数> \
  --report-json production/silent-master-check.json
```

`--expect-frames` 会完整解码精确统计帧数，容器声明的 `nb_frames` 可能缺失或不准，不能用来断言时间轴。`--decode` 发现截断和损坏：`moov` 在文件头时，截断一半的文件照样能读出正确规格。判断结果读 `--report-json` 落盘的文件，不要解析终端输出。

## 第九阶段：选择 BGM 和 SFX

### 素材选择

- BGM 应与内容气质一致，优先选择克制、不会遮挡讲述的氛围音乐。
- SFX 只用于开场钩子、信息出现、确认反馈、关键转场和结论等明确节点。
- 不要为每个元素移动都添加音效。
- 音效挂在它回应的那个画面事件上，不挂在镜头切换上：不要因为又切了一次镜头就重复同一个音效，重复会把这个音效的含义用完。
- 音乐在人名、数字和关键论断处让位；刻意的安静同样是表达，不要用音乐把每一段空白都填满。
- 只使用授权明确且来源可追溯的素材。
- 下载前记录作品页、作者、素材 ID、授权页面和是否存在额外限制。
- 保存原始下载文件和解码后的工作文件，并计算 SHA-256。

在 `production/audio/asset-ledger.json` 中记录：

- 本地文件名和用途。
- 原始 URL、作者、素材 ID。
- 授权类型和授权证据路径。
- 下载时间和 SHA-256。
- 是否需要署名及建议署名文字。

### Cue Sheet

建立 `production/audio/cue-sheet.json`：

- 每个 cue 必须以整数帧定位，再换算为音频采样点。
- 记录素材、开始帧、持续时间、增益、淡入淡出和用途。
- 每条音效 cue 写明它回应的 `event_id`（取自 `visible-event-inventory.json`）或 `phrase_id`（取自 `phrase-timeline.json`），开始帧由该事件或短语的帧号推出，不另行手估；说不出回应哪个事件的音效删掉。
- 不允许 cue 越过节目终点。
- 同一时间的音效数量应受控，避免高频叠加和听觉拥挤。

## 第十阶段：配音优先的声音混合

- 统一使用 48kHz 双声道工作格式。
- 总音频每声道采样数必须由最终节目时长精确推导。
- 人声保持原速；画面需要更长展示时用停顿和音乐延展，不拉慢人声。
- BGM 从开场自然淡入，在结尾自然淡出；不要硬切。
- 使用侧链或等效自动压低，让有人声时的 BGM 明显低于无人声区间。
- SFX 的存在感服从信息层级，不能覆盖关键词、数字、模型名或结论。
- 用 FFmpeg `amix` 混多路时显式写 `normalize=0`。默认会按输入路数压低每一路，混出来整体偏小，后面的响度规范化只能硬拉；各路电平应由前面的增益与闪避决定。
- 控制峰值并执行两遍响度规范化。若无平台专用规范，以视觉规范 `audio` 块的 `final_integrated_lufs`、`final_lufs_tolerance` 和 `true_peak_max_dbtp` 为检查目标，而不是盲目追求更响。
- 成片 AAC 码率不低于 `384k`。低码率会在编解码时把采样之间的峰值抬高，瞬态密集的音乐能抬 3 dB 以上：`premaster.wav` 达标不代表成片达标，真峰要量编码后的候选成片。
- AAC 编码后必须按解码得到的真实采样数检查节目终点，不能只相信容器显示的舍入时长。

至少生成：

- `production/audio/voice.wav`
- `production/audio/music.wav`
- `production/audio/sfx.wav`
- `production/audio/premaster.wav`
- `production/audio/candidate.mp4`
- `production/audio/machine-qc.json`
- `production/audio/sound-checklist.json`

## 第十一阶段：最终验收、试听与交付

候选成片必须通过以下检查：

### 机器检查

画幅、帧率、编码、像素格式、总帧数、音轨规格、可完整解码和第 0 帧封面这几项由一条命令完成，不要自己拼 FFprobe：

```bash
am validate video <候选成片> --audio --expect-frames <冻结时间轴总帧数> \
  --decode --check-frame-zero --report-json production/final-check.json
```

不符项会一次全部列出。判断结果读 `--report-json` 落盘的报告，`ok` 为 `false` 时 `problems` 数组里是全部不符项，`hints` 里是值得看一眼但不构成失败的观察（例如第 0 帧偏暗）。

### 整片复查

机器检查只看规格，拼接与混音之后才出现的问题要靠整片复查：

```bash
am review <候选成片> --scenes scenes/ --out production/review
```

它比对音轨与视频流各自的时长，精确统计总帧数并与各镜头帧数之和比对（漏拼、重拼、掉帧都会被拦下），检查每个镜头交界两侧的帧，并把每个镜头的首帧、中间帧、末帧拼成联系表，每个交界再出一张前后各 6 帧的条带 `seam-NN.png`。判断结果读 `production/review/review.json`：`ok` 为 `false` 时 `problems` 里是全部不符项；交界处的纯色帧只进 `hints`，因为切黑、切白可能是有意的转场，要你看图确认。有音轨时它还会解码成片，测量积分响度、真峰与响度范围写进报告的 `loudness`，并与视觉规范 `audio` 块的目标比对；偏离只进 `hints`，不影响退出码，但除非平台另有响度规范，出现了就回第十阶段重新混音，不要在成片上再叠一次规范化。

机器检查通过后逐页打开 `production/review/sheet-NN.png` 做视觉复查：相邻镜头的风格、配色与画面密度是否一致，结尾是否停在合适的画面；再逐张看 `seam-NN.png`，确认每个交界的转场顺畅，没有一帧闪烁、跳变或停在入场中间态，并对照两侧镜头 `prompt.md` 声明的出场状态与入场状态：持续物件的位置、大小和状态是否真的接上了。发现问题只重渲染相关镜头，重新拼接、混音后再复查一次。复查报告与联系表纳入审批绑定的检查文件。

整片看一遍时，对每一处画面变化问一句：它是在澄清、证明、控制节奏还是加强情绪？说不出作用的变化删掉。同时对照 `treatment.md` 检查钩子、揭示和收束是否落在了预定的位置。

发现问题按这个顺序修：

1. 先定位它归哪个文件管：稿件、`treatment.md`、`scene-plan`、某个镜头的 `prompt.md`、`frame.md`、cue sheet 或混音参数。
2. 改那个源头，再重新生成它的下游，不在成片上打补丁。
3. 改了一轮没有变好，先回头修正对问题原因的判断，写进修复记录，再动第二轮；不要在同一个判断上反复加码。

命令覆盖不到、仍需另行确认的：

- 音频解码采样数和节目终点正确。
- 字幕、镜头和音效 cue 没有越界。
- 发布平台若有专用响度规范，按平台规范核对 `review.json` 的 `loudness` 实测值。
- 输入、候选、素材、日志和检查报告的 SHA-256 绑定一致。

### 人工试听

机器检查不能替代人工听感。发布前必须：

- 用耳机完整试听一次。
- 用手机外放完整试听一次。
- 重点检查断句、发音、人声清晰度、音乐遮挡、音效突兀、爆音、点击声和结尾硬切。
- 无法实际完成物理设备试听时，必须在检查表中标记“未执行”，不得假称通过，并明确建议发布前补做。

### 审批和原子交付

- 把最终候选及所有检查文件的 SHA-256 写入 `production/approval.json`；`treatment.md` 与 `scene-plan` 一并绑定，它们改动后已批准的候选随之失效。
- 只有审批绑定全部通过时，才允许交付。
- 当前目录已有 `final.mp4` 时，先保留为带版本或时间标识的备份。
- 使用同文件系统内的临时文件和原子重命名生成根目录 `final.mp4`。
- 原子替换后再次检查 `final.mp4` 的哈希、规格和完整解码。

### 修订版与候选生命周期

- 每次修订使用独立 revision 或 attempt 目录，保留上一版视频、日志、检查报告和 SHA-256。
- 每次声称“已经修复”前，必须实际生成新的候选 MP4，并汇报新路径、修改时间、规格和 SHA-256；没有新视频不得只凭源码或截图宣称完成。
- 用户审阅的是明确绑定哈希的候选。预览通过后才能执行高质量渲染；高质量渲染不得改变已批准的时间轴、文字或布局。
- 只修复受影响镜头，其他已冻结镜头必须按帧数和哈希复用。拼接后仍要重新执行全片语义和机器 QC。

## 第十二阶段：刷新发布配置

### 平台封面

交付前按发布平台另做封面图。这些是上传时单独提交的封面，不能拿成片截帧代替：截帧的构图是按 1920×1080 排的，换成另一个比例必然裁掉标题或主体。

| 平台 | `platform` | 要出的封面 | 推荐尺寸（宽 × 高） | 构图要点 |
|---|---|---|---|---|
| 抖音 | `douyin` | 横 4:3 + 竖 3:4，两张都要 | 宽 1440、高 1080；宽 1080、高 1440 | 竖封面用于个人主页与双列信息流 |
| 小红书 | `xiaohongshu` | 竖 3:4（也接受 1:1、4:3） | 宽 1080、高 1440 | 其他比例会被裁成 3:4 或 4:3 |
| 微信视频号 | `channels` | 竖 3:4（横版视频可加 16:9） | 宽 1080、高 1440 | 主体与标题集中在中间 |
| B 站 | `bilibili` | 横 16:10 | 宽 1920、高 1200 | 各展示位会再裁成 16:9 或 4:3：标题与主体放进居中的宽 1600、高 1080 安全区 |
| 知乎 | `zhihu` | 横 16:10 | 宽 1920、高 1200 | 同 B 站 |

同一张 3:4 竖封面可以同时登记给抖音、小红书与视频号；B 站与知乎的 16:10 必须单独构图，不能拿 4:3 拉伸。

1. 封面沿用本片的视觉规范与画面主体：同一套配色、字体与角色/场景，定格在全片最有戏剧性、最能说明主题的一刻。标题用已确认的封面标题，必要时加一到两个短钩子（数字、反差），不新增片中没有的事实。
2. 文件命名为 `covers/<平台>-<宽>x<高>.png`，例如 `covers/douyin-1440x1080.png`、`covers/bilibili-1920x1200.png`。源文件放在 `covers/src/<同名>/index.html`，各自是一个 `data-duration` 为 1 秒的 HyperFrames 合成。字体与脚本复制进各自目录、用目录内路径引用，不得用 `../` 引用目录外的文件，否则字体与脚本不会被加载。
3. 每种比例分别构图，不要把一张缩放成另一张：横版通常文字在左、主体在右，竖版通常标题在上、主体在中、钩子在下。标题与主体都不得出画，文字不得压在主体上。
4. 每张先跑 `npx --yes hyperframes@0.8.14 check covers/src/<目录>` 直到通过，再用 `snapshot --at 0.5` 出图，复制为上面的文件名，打开看一遍。
5. 在 `publish.md` 的 `cover.images` 里逐张登记 `{platform, ratio, path, width_px, height_px}`，`cover.source_type` 写 `designed_image`；「封面文字」章节写出封面标题并列出每张图的路径。`am validate publish` 会按上表核对平台是否接受该比例、实测尺寸与比例、短边不小于 1080，并在 `ready` 时检查发布平台要的封面是否齐全。封面预览（`manual_checks.cover_preview`）仍需人工在平台上确认。

### 发布配置

最终交付后刷新根目录 `publish.md`：

- 用 FFprobe、完整解码和 SHA-256 实测 `final.mp4`，填入实际规格和最终哈希；新视频与新哈希验证完成后才把 `video.replacement_in_progress` 恢复为 `false`。
- 从真实项目文件填入发布平台、主标题、介绍、话题、已确认封面文字、素材署名和 evidence。署名必须由素材账本和授权证据支持，不得凭印象补写。
- 读取 `production/approval.json` 的 `expected_delivery.sha256`，并在存在时校验 `delivery_verification.actual_sha256`。它们必须与 `publish.md` 和实际成片哈希一致。
- 可复用工具不得硬编码某个 revision 名称、旧总帧数、旧总采样数或旧候选哈希。当前合同值必须从活动的 `timing-report.json`、`phrase-timeline.json` 和 `approval.json` 推导；只有不可变备份的哈希可以显式冻结。
- 将耳机、手机外放、封面预览、版权确认四项人工检查写入 `manual_checks`；机器检查不能代替人工检查。
- 按合同计算且只使用 `draft`、`pending_manual_checks`、`blocked`、`ready`。没有成片或非人工字段/证据缺失时用 `draft`；替换、解码、哈希、规格、审批、失败检查或署名证据异常时用 `blocked`；只有非人工信息完整且仍有适用检查待执行时才用 `pending_manual_checks`。
- YAML 必须使用支持安全模式的 YAML 库序列化。先在与目标文件同一文件系统写临时文件，执行 `am validate publish <临时文件> --project-root .`，验证通过后才原子重命名为 `publish.md`。
- 不得用无效临时文档覆盖最后一个有效 `publish.md`。如果无法构造合同有效的状态文档，保留旧文件并在最终汇报中明确刷新失败，绝不声称 `ready`。

## 最终交付清单

- 根目录 `final.mp4`。
- 根目录 `publish.md`，包含最终发布文案、成片 SHA-256、证据索引、署名和人工待办。
- `covers/` 下按发布平台规格出的封面图（抖音横 4:3 + 竖 3:4、小红书与视频号竖 3:4、B 站与知乎横 16:10）及其 `covers/src/` 源文件，已登记进 `publish.md` 的 `cover.images`。
- 最终配音对应的 `transcription-production.srt`。
- `production/script-final.md` 和修改记录。
- `production/treatment.md` 整片处理方案，以及从它推出的 `production/scene-plan.json` 与 `production/scene-plan.md`。
- `production/input-manifest.json`、制作配置和时间报告。
- 每个镜头的渲染日志、源项目证据和镜头 MP4。
- 静音母版、音频 stems、cue sheet、素材账本和授权证据。
- 视觉检查表、声音检查表、机器 QC 和审批文件。
- 失败镜头的编号、失败阶段、关键日志和建议重试方式。
- 发布所需的音乐或素材署名文字。

完成时汇报最终成片路径、`publish.md` 路径、封面图路径、时长、分辨率、帧率、音频规格、SHA-256、发布状态、所有检查结果，以及仍需人工完成的发布前事项。

## 第十三阶段：项目收尾与归档

最终交付和检查结果汇报完成后，询问用户是否现在归档本项目并把工作目录复位到 `main`。归档不是成片验收的一部分；用户未明确确认时，不得运行归档。

用户确认后，先在仓库根目录运行：

```bash
am archive --dry-run
```

如果 dry-run 检出相对 `main` 的共享文件修改，或发现 ignored（被忽略）文件，立即停止，不得绕过检查、移动文件或重置工作目录。原样汇报脚本列出的问题，并请用户决定应该删除、保留、拆分还是归档这些文件；问题处理完后再重新 dry-run。

dry-run 无阻断时，向用户展示归档计划，然后运行 `am archive`，保留脚本的交互式确认提示；除非用户明确要求非交互执行，否则不要使用 `--yes`。成功后汇报归档路径、原项目分支和提交、复位后的 `main` 提交及工作区状态。不得删除旧项目分支或 Git 历史。

## 歌曲讲解分支

若项目根存在 song.yaml，音频制作、时间轴与合成改按 PROMPT-SONG.md 执行；
其余画幅、风格、MG 渲染、视觉验收与发布要求继续适用。
