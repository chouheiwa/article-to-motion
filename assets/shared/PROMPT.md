读取当前工作目录的 `transcription.srt`，按语义拆分镜头，为每个镜头创建独立工作目录，并发调度渲染工具制作 MG 动画，最后用 ffmpeg 按镜头顺序拼接并交付 `final.mp4`。

## 工作边界

- 不设计镜头内部的具体 MG 动效，也不替渲染工具写动画方案；渲染工具负责单个镜头的创意、代码实现和 mp4 渲染。
- 你负责调用渲染工具，为每个镜头编写 `prompt.md`。可以根据当前镜头文案自由撰写创意方向，不要求逐字沿用默认创意正文。调整目标是让单个镜头的艺术效果、视觉概念和动效表达更贴合文案。
- 不得改变 `am` 保证的执行契约：非交互式运行、输出文件名、完整字幕路径、阶段性汇报格式、日志过滤方式和最终 mp4 交付要求必须保持确定。

## 渲染器配置

- 开始前读取仓库根目录的 `article-to-motion.conf`，并考虑当前环境变量或 `.env` 的覆盖值，确定本次使用的 `RENDERER`。
- 把解析后的渲染器记录在执行日志中。每个镜头用 `am scene run <镜头目录>` 调用，渲染器由 `am` 从配置解析；需要单独指定时写入该镜头 `scene.json` 的 `renderer` 字段。
- 如果配置的渲染工具不可用，停止并明确报告缺失的 CLI；未经用户确认不得替换渲染器。

## 镜头拆分

根据 `transcription.srt` 做粗粒度分镜。一个镜头可以包含单条字幕，也可以合并连续多条字幕；合并依据是文案是否表达同一主题、同一因果关系或同一视觉概念。

每个镜头必须对应一个连续时间区间，所有镜头按顺序首尾相接，完整覆盖 `transcription.srt` 从第一条字幕开始到最后一条字幕结束的总跨度。字幕之间的无文字空白不能丢失，应并入前一个镜头作为停顿、收尾或转场；空白很长时也可以单独做静默/转场镜头。

每个镜头的时长用“秒”作为单位，保留 SRT 毫秒精度，写成小数秒，例如 `2.833`、`6.500`，不要四舍五入或截断为整数秒。调用渲染工具前必须复核：所有镜头 `scene.json` 中 `duration_seconds` 之和应等于 `最后一条字幕结束时间 - 第一条字幕开始时间`，误差不超过 0.1 秒。

## 镜头目录

为每个镜头创建独立目录，建议使用 `scenes/scene-001`、`scenes/scene-002` 这样的命名。目录至少包含：

- `scene.json`：执行契约。字段为 `id`、`duration_seconds`、`output`、`transcript`、`text`，可选 `style_guide` 和 `renderer`。未知字段会直接报错。
- `prompt.md`：本镜头的创意方向、视觉概念和表达重点。可以按镜头文案自由改写，交付规格、阶段消息等执行契约由 `am` 保证，不在本文件中。
- `transcription.srt`：复制完整字幕文件，供渲染工具理解整体上下文。
- `frame.md`：仓库根目录存在视觉规范文件时必须一并复制，并在 `scene.json` 的 `style_guide` 中声明。声明了但文件不存在会报错。
- `assets/fonts/`：把视觉规范 `typography.font_files` 声明的字体文件按相同相对路径复制进来。渲染机是一台干净的无头 Chrome，不装任何系统字体，中文字形只能由这些自带文件提供；字体栈里没有 `@font-face` 的字体族会静默回退，本地看着正常、成片排版却是错的。
- 渲染器锁定文件：固定 HyperFrames 版本、禁止升级或改动已装技能、禁止读取 `.env`、禁止改动 `scene.json` 的执行契约。文件名取决于该镜头所用渲染工具会自动读取的项目级指令文件（例如 `claude` 读 `CLAUDE.md`），按所选工具各自的约定命名。

全部技能都在项目根的 `.agents/skills/` 下，由 `am init` 一次装齐：HyperFrames 官方技能按固定版本安装，`text-to-lottie` 与 `algorithmic-art` 随 `am` 二进制下发。技能缺失时重跑 `am init` 补齐，不要自己跑 `npx hyperframes skills`——那会装到本机 HOME 而不是项目里，绕过版本固定。也不得从其他项目或本机目录复制。

锁定文件如果限制「只在本镜头目录内工作」，必须给项目根的 `.agents/skills/` 留读取例外——它在项目内，但在镜头目录之外，由 `am` 写进渲染提示词。少了这条例外，渲染工具会拒读动效 rule 索引，动画退化成只有淡入和位移。

用 `am scene run scenes/scene-001` 执行单个镜头。渲染器从仓库配置解析，无需显式传递；解析不到会直接报错，不存在静默回退。命令会在启动渲染前打印这一镜实际使用的渲染器及其来源（仓库配置或 `scene.json` 覆盖），把这一行记进执行日志。

## 调度和等待

用 `am scene run-all scenes/ --srt transcription.srt --strict-coverage` 一次性调度全部镜头。并发数由配置 `SCENE_JOBS` 决定（默认 3），可用 `--jobs N` 覆盖。镜头之间没有创作依赖，可以乱序渲染；跨镜头视觉一致性由 `frame.md` 保证，与执行顺序无关。

`--srt` 加 `--strict-coverage` 会在开始渲染前核对镜头编号唯一性，以及镜头总时长是否等于字幕跨度（容差 0.1 秒）。务必带上：漏掉一镜或某镜时长写错，等全部渲染完拼接时才发现的话，每一镜都已经烧掉一次完整的渲染调用。

已有合格产物的镜头会被跳过。若某个镜头的 `prompt.md` 或 `scene.json` 比产物新，会被跳过并给出警告，需要重渲染时用 `am scene run <镜头目录>` 单独执行。

渲染器非零退出会自动重试（默认 2 次，退避 10 秒与 30 秒），失败证据保留在该镜头的 `attempts/attempt-NN/`。产物校验失败不会重试——那通常意味着提示词或时长声明有问题，需要你先修再重跑。

产物校验查的是画幅、帧率、时长和「必须无音轨」四项。画幅取自 `frame.md`，不符时不要试图缩放补救——回到该镜头改提示词重渲染。

产物校验还会静态扫描镜头合成源码：读时钟、`Math.random()`、`requestAnimationFrame`、`setInterval`、无限 `repeat`、渲染时联网、在线字体与未固定版本的 CDN 库都会让同一帧在两次渲染间不同，命中即判失败且不重试。改完合成代码、重渲染前可用 `am scene lint <镜头目录>` 先确认。

## 视觉检查

渲染成功不等于画面正确：文字压在图形上看不清、中文字体静默回退成方框、动画在中途就停住、转场只做了一半——这些都能拿到退出码 0。

每个镜头渲完后用 `am scene frames <镜头目录>` 抽帧，默认在首帧、四分位和末帧各抽一张 PNG，然后逐张打开看。第 0 帧要做封面时加 `--at 0 --check-blank`，空白帧会直接判失败。

抽帧命令只负责把 mp4 变成可以看的 PNG，并标出纯色帧这种机器能判的情况；画面对不对要你自己看图判断。发现问题只重渲染相关镜头。

调度时加上 `--report-json production/run-report.json`，得到一份机器可读的执行报告，不要靠解析屏幕上的中文汇总来判断结果。报告结构：

```json
{
  "schema_version": 1,
  "seconds": 5412.3,
  "interrupted": false,
  "exit_code": 1,
  "counts": {"succeeded": 9, "skipped": 2, "stale": 0, "failed": 1},
  "scenes": [
    {"scene_id": "scene-001", "status": "succeeded", "attempts": 1, "seconds": 612.4, "reason": ""},
    {"scene_id": "scene-004", "status": "failed", "attempts": 3, "seconds": 1801.2, "reason": "渲染器退出码 3"}
  ]
}
```

`status` 取值为 `succeeded` / `skipped` / `stale` / `failed`。被中断时报告照常落盘，`interrupted` 为 `true`，据此判断哪些镜头还需要重跑。汇报失败镜头时直接引用 `scene_id` 和 `reason` 字段。

使用 `am` 定义的阶段性汇报规则；`am scene run` 只放行以 `[[USER_MESSAGE]]` 开头的消息给你和用户。

如果一段时间没有新的 `[[USER_MESSAGE]]` 输出，不要立即判定失败；先检查：

- `render-<scene>.stream.jsonl` 和 `render-<scene>.stderr.log` 是否仍在写入。
- 项目文件、渲染目录或 mp4 文件是否有更新时间。
- 是否存在 hyperframes、ffmpeg、Chromium 或 Node 渲染进程。
- `am scene run` 的最终退出码。

只有在进程退出失败，或长时间无日志、无文件更新且无渲染进程时，才判定该镜头失败并记录失败原因。

## 发布配置交付

除视频外，根目录必须交付 `publish.md`。以 `templates/publish.md` 为结构参考，但不得把模板当成已经填写的项目结果。

- 完成字幕分析后先生成 `workflow: basic_srt` 的草稿。标题、介绍、话题和封面候选必须来自完整终稿 SRT；平台未知时写 `unspecified`。
- 封面只可标记为用户已确认的 `confirmed_config`、第 0 帧实测得到的 `detected_frame_zero`，或尚待确认的 `generated_candidate`。候选未确认时保持 `draft`。
- 最终视频为静音时写 `audio_codec: none`，耳机和手机外放检查使用 `not_applicable` 与 `no_audio_track`。不存在的审批、账本或检查证据保持空值或 `pending`，并在未完成事项中明确列出。
- 如果将替换已有 `final.mp4`，先保留旧哈希，将 `video.replacement_in_progress: true`，以 `video_replacement_in_progress` 原子刷新为有效 `blocked` 文档；新视频、最终 SHA-256 和规格全部验证后才能恢复为 `false`。
- 使用 FFprobe 和完整解码读取实际成片规格并计算最终 SHA-256。没有成片时保留包含 `final_video_missing` 的有效 `draft`；成片无法解码或存在哈希/规格异常时，生成含对应诊断的有效 `blocked` 文档。
- YAML frontmatter 必须使用支持安全模式的 YAML 库序列化，不得拼接未转义外部文本。临时文件与 `publish.md` 必须位于同一文件系统。
- 每次生成或刷新都先对临时文件执行 `am validate publish <临时文件> --project-root .`；只有验证通过后才原子重命名为根目录 `publish.md`。无效临时文件不得替换现有文件；保留最后一个有效版本，汇报刷新失败，且不得声称 `ready`。

## 最终交付

所有镜头 mp4 完成后，用 `am concat scenes/ --out final.mp4` 按镜头顺序拼接。规格一致时全程 `-c copy` 不重新编码；只有编码或像素格式不同的片段会先生成规范化副本，原始文件保留。只有一个镜头时同样用这条命令。

先跑一次 `--dry-run` 看有没有规格不一致的镜头。分辨率或帧率不符会直接失败并要求回镜头工程重渲染——不要用 `setpts`、缩放或改帧率把它糊过去，那会动到时间轴或损失画质。

拼完后用 `am validate video final.mp4 --silent --decode --check-frame-zero` 做一次机器验收，不通过就不要交付。

再用 `am review final.mp4 --scenes scenes/ --silent --out production/review` 做整片复查：它精确统计总帧数并与各镜头帧数之和比对（漏拼、重拼、掉帧都会被拦下），检查每个镜头交界两侧的帧，并把每个镜头的首帧、中间帧、末帧拼成联系表。判断结果读 `production/review/review.json`；机器检查通过后逐页打开 `sheet-NN.png`，看相邻镜头的风格与密度是否一致、交界是否跳变突兀、结尾是否停在合适的画面。有问题只重渲染相关镜头，重新拼接后再复查一次。

最终交付：

- 每个镜头目录中的渲染日志和镜头 mp4。
- 拼接后的 `final.mp4`。
- 根目录 `publish.md`，包含实际成片规格、最终 SHA-256、发布文案和未完成事项。
- 如有失败，提供失败镜头编号、失败阶段、关键日志和建议重试方式。

## 项目收尾与归档

最终交付和检查结果汇报完成后，询问用户是否现在归档本项目并把工作目录复位到 `main`。归档不是成片验收的一部分；用户未明确确认时，不得运行归档。

用户确认后，先在仓库根目录运行：

```bash
am archive --dry-run
```

如果 dry-run 检出相对 `main` 的共享文件修改，或发现 ignored（被忽略）文件，立即停止，不得绕过检查、移动文件或重置工作目录。原样汇报脚本列出的问题，并请用户决定应该删除、保留、拆分还是归档这些文件；问题处理完后再重新 dry-run。

dry-run 无阻断时，向用户展示归档计划，然后运行 `am archive`，保留脚本的交互式确认提示；除非用户明确要求非交互执行，否则不要使用 `--yes`。成功后汇报归档路径、原项目分支和提交、复位后的 `main` 提交及工作区状态。不得删除旧项目分支或 Git 历史。
