# 多角色叙事作业规程（cast 模式附录）

本附录只在项目根存在 `cast.yaml` 时生效——`cast.yaml` 存在与否就是叙事模式本身，不需要另外判断任何开关。它覆盖 `PROMPT-PRODUCTION.md` 第三阶段（TTS 和断句校对）与第四阶段（依据实际语音重建 SRT）里与"整段配音、单一说话人"相关的条款；其余阶段——稿件盘点、首帧封面、BGM/SFX、混音、最终验收、归档——不受影响，仍按 `PROMPT-PRODUCTION.md` 原文执行。

没有 `cast.yaml` 的单口播项目忽略本文件全部内容。

## 适用范围与生效条件

- 触发条件只有一个：项目根存在 `cast.yaml`。不存在则本附录不适用。
- 出场角色必须先通过 `am cast add <外部角色包目录>` 或 `am cast new <id>` 登记进 `cast.yaml` 的 `packs` 列表，才能出现在对话体脚本和 `scene.json` 的 `cast` 块里。
- `am init --narration cast` 建出的 `cast.yaml` 里 `packs` 是空列表——这是预期状态，不是错误。项目在第一次 `am cast add` / `am cast new` 之前本来就还没有可用角色，此时若强行渲染角色相关镜头会在角色装载阶段清楚报错，好过带着不存在的角色渲到一半才发现。
- `am init --narration cast` 同时会建出空的 `cast/` 目录。Git 不跟踪空目录，`git status` 在第一次 `am cast add` / `am cast new` 之前不会显示它——这是正常现象，不代表目录没建成功；目录一旦被写入第一个角色包（即目录不再为空），Git 就会正常跟踪其内容。

## 一、对话体脚本格式

终稿从单口播的自然段落改为对话体脚本：每一行一个说话人的一句或一段台词，格式为

```
<角色 id>: <台词>
```

角色 id 必须是已登记进 `cast.yaml` 的角色包 id（即 `cast/<id>/character.yaml` 里的 `id`）。示例：

```
heiwa: 今天我们来看看这个新功能到底解决了什么问题。
zhaocai: 喵，这个我熟，说来听听。
heiwa: 你先别急，我们从背景讲起。
```

- 出现未登记的角色 id 是脚本错误，必须先 `am cast add` / `am cast new` 引入角色再继续，不得先写脚本再补角色。
- 稿件审核关卡的检查项与单口播一致（预计朗读时长、开场全文、与原稿的主要变化、需要保护的专有名词和不可拆分短语），额外增加：各说话人的台词量是否严重失衡、每个角色的用词和语气是否符合人设。
- 稿件未确认时，不进入正式分段 TTS。

## 二、按说话人连续段分段，不按字幕行

分段单位是"同一说话人连续说的一段话"：只有说话人切换才起新段，一段内允许包含多句后续会拆成多条字幕的文本。

不按字幕行分段的理由：逐条字幕单独调一次 TTS，语调、呼吸和重音在人为切点处被打断，同一个人一整段连贯发言会被合成成一串听感割裂的短句拼接，是本附录反复强调的"语调碎裂"问题。整段合成才能让停顿、重音落在语义该在的地方。

- 说话人切换处必须切段，哪怕上下两句在内容上是一次紧密的问答呼应，也不能把两个说话人的台词合并进同一次 TTS 调用。
- 一段内部如果因为过长需要分批合成，仍必须保持"这一批只属于同一个说话人"，不得为了缩短单次请求而把说话人切换点藏进一段里。

## 三、逐段调用 mmx / bl，保存段内时间戳

TTS 服务提供方仍由 `article-to-motion.conf`（或环境变量 `TTS_PROVIDER`）决定，各路径的凭据、鉴权方式、密钥保管规则与 `PROMPT-PRODUCTION.md` 第三阶段完全一致，此处不重复；音色、语速试听确认，以及易误读词发音控制（`mmx` 的 `--pronunciation`、百炼 SSML `<phoneme>`，不支持自定义发音的路径改写送进 TTS 的文本）的要求也一致，只是作用范围从"整篇口播"缩小到"每一段"。

- 每个说话人使用其 `cast/<id>/character.yaml` 里 `voice.<TTS_PROVIDER>.voiceId`（例如 `voice.minimax.voiceId`、`voice.edge.voiceId`，取决于当前 `TTS_PROVIDER`）对应的音色；同一说话人从头到尾使用同一个 voiceId，不得中途切换。
- 逐段合成时依旧按第三阶段的方式取真实时间戳：MiniMax 用 `--subtitles` 直接产出，百炼走合成 + `bl speech recognize` 两步识别。这里拿到的时间戳是相对本段音频起点的段内时间，不是全局时间轴——全局时间轴由 `am dialogue assemble` 统一计算，不要在这一步手工换算或累加偏移。
- 每段落盘为独立音频文件，建议命名 `production/audio/segments/<index>-<speaker>.wav`，`index` 从 0 开始，与后面 `plan.json` 里 `segments[].index` 一一对应。
- 断句检查、发音控制覆盖范围的要求与单口播一致，只是逐段各自核对：产品名/模型名是否被拆开、数字和英文缩写是否读对、长句是否留出呼吸、段尾是否留出画面展示时间。

## 四、写 `production/audio/plan.json`

`schema` 固定为 `cast-dialogue/v1`（与装配产物 `production/dialogue.json` 共用同一个 schema 标识）。结构示例：

```json
{
  "schema": "cast-dialogue/v1",
  "segments": [
    {
      "index": 0,
      "speaker": "heiwa",
      "voiceId": "voice-heiwa-01",
      "audio": "production/audio/segments/0-heiwa.wav",
      "gapAfterMs": 240,
      "lines": [
        { "text": "今天我们来看看这个新功能到底解决了什么问题。", "startSeconds": 0.0, "endSeconds": 2.8 }
      ]
    },
    {
      "index": 1,
      "speaker": "zhaocai",
      "voiceId": "voice-zhaocai-01",
      "audio": "production/audio/segments/1-zhaocai.wav",
      "gapAfterMs": 240,
      "lines": [
        { "text": "喵，这个我熟，说来听听。", "startSeconds": 0.0, "endSeconds": 1.6 }
      ]
    }
  ]
}
```

- `segments` 按最终播放顺序排列，`index` 从 0 递增且不留空、不重复。
- `speaker` 必须是 `cast.yaml` 登记班底里的角色 id。
- `lines[].startSeconds` / `endSeconds` 是本段音频内部的相对时间（来自上一步的段内时间戳），不是全局时间。
- `gapAfterMs` 是这一段结束到下一段开始之间要插入的静音时长，单位毫秒：常规轮换取 `cast.yaml` 的 `defaults.gap_ms.turn`，抢话/打断式的衔接取 `defaults.gap_ms.interject`。最后一段没有下一段，字段仍要填写，装配时会被忽略。

## 五、调用 `am dialogue assemble`

```
am dialogue assemble
```

- 默认读取 `production/audio/plan.json`，可用 `--plan` 指定别的路径。
- 产出三个文件：`production/audio/voice.wav`（拼接后的完整配音）、`transcription-production.srt`（按全局时间轴生成的字幕）、`production/dialogue.json`（装配后的结构化时间线——里面的 `segments` 与 `lines` 都已经是全局时间，不再是段内相对时间）。
- 命令内置两道漂移断言（逐段声明时长与实测时长的比对、总时长漂移比对），任一超出容差都会失败并点名具体是第几段——原样报告给用户，不要自己吞掉重试或手工拼凑时间轴掩盖问题。
- 装配完成后，`PROMPT-PRODUCTION.md` 第四阶段其余条款（短语级语义时间轴、`timing-report.json`、`closingHoldFrames` 等）照常执行，只是输入换成这里产出的 `production/dialogue.json` 与新的 `transcription-production.srt`。

## 六、拆镜头时给 `scene.json` 填 `cast` 块

拆镜头的分段规则（按语义合并、时长精度、总时长核对）与 `PROMPT-PRODUCTION.md` 第五阶段一致。**只要镜头覆盖的时间段里有人说话，就要在 `scene.json` 里追加 `cast` 块**——角色是否出场决定的是 `on_stage` 写什么，不决定要不要写 `cast` 块：

```json
{
  "cast": {
    "pack_dir": "cast",
    "ground_y": 0.78,
    "on_stage": [
      { "id": "heiwa", "x": 0.32, "pose": "idle", "facing": "right" },
      { "id": "zhaocai", "x": 0.68, "pose": "idle", "facing": "left" }
    ]
  }
}
```

- `pack_dir` 是相对本镜头目录的路径，**不得指向镜头目录之外**（例如 `../../cast` 这种写法会被 `am validate cast` 与渲染前的镜头级校验一律拒绝：渲染只服务镜头目录内的文件，外部路径静默 404、角色不出现且不报错）；正确做法是把项目根 `cast/<id>/` 拷贝一份进本镜头目录（例如 `<镜头目录>/cast/<id>/`），`pack_dir` 填这份拷贝相对本镜头目录的路径（上例是 `cast`）。`on_stage` 列出本镜头台上的全部角色及初始站位（`x`，画面归一化比例）、初始姿势（`pose`）和朝向（`facing`）；`ground_y` 默认沿用 `cast.yaml` 的 `defaults.ground_y`，除非本镜头有特殊构图需要单独覆盖。
- **不要手写 `beats`，一律执行 `am dialogue beats` 生成。** 上面的例子里没有 `beats` 字段，这是对的：把全部镜头的 `duration_seconds`、`pack_dir`、`ground_y`、`on_stage` 都填好之后，在项目根执行一次

  ```bash
  am dialogue beats
  ```

  它读 `production/dialogue.json` 与全部 `scenes/*/scene.json`，按镜头顺序累加时长得到每个镜头的全局起点，把每一行台词切进对应镜头并减去该镜头起点，写回各镜头的 `cast.beats`。**这个减法不给你做**：算错了不会报错，成片画面正常，只是角色在不该说话的时候动嘴。**没有执行过这条命令不得开始渲染。**
- 改过任何镜头的 `duration_seconds`、增删过镜头、或重跑过 `am dialogue assemble` 之后，**都要重新执行一次 `am dialogue beats`**。命令是幂等的，重复执行结果一致，内容没变的文件不会被重新落盘。**既有的 `beats` 一律被忽略并整体重写**——它是这条命令的输出，不是它的输入，所以「把某镜头时长改小、旧节拍越界」这种中间状态不需要你先手工清理，直接重跑即可（这期间 `scene.json` 确实是不合法的，`am validate cast` 与渲染都会拒绝，重跑一次就恢复）。
- 命令会替你处理这些情况，不需要自己判断：一句台词横跨镜头切点时按切点拆成多拍分别落进各自镜头（换算回全局时间后首尾相接，`am validate cast` 认得出这是完整的一行，不需要把台词本身拆开）；有 `cast` 块但整段没人说话的镜头写出空数组 `"beats": []`；`beats` 按 `start` 递增排列、互不重叠；`pack_dir` / `ground_y` / `on_stage` 与 `cast` 块之外的一切字段原样保留。
- 命令报错的两种情况都要你去改镜头，别绕过去：**某个镜头有台词盖过却没写 `cast` 块**——补上 `cast` 块（`beats` 不用写）再重跑，命令不会替你决定角色站位；**某一行台词落在所有镜头覆盖的时间范围之外**——那是镜头 `duration_seconds` 之和与配音对不上，先对齐镜头时长再重跑。任一检查不通过，命令一个文件都不会写。
- **一条 beat 的含义是「这段时间这个人在说话」，不蕴含「他出现在画面里」。** `speaker` 不必出现在 `on_stage` 里：不在台上就是画外音，渲染提示词会把这几拍明确标成「（画外音）」并要求渲染 agent 不得把这个角色画进画面。画外音说话人的台词同样会被写进 `beats`，不会因为他不在台上就被跳过。`speaker` 唯一的硬性要求是能在本镜头 `pack_dir` 下找到对应角色包（与 `on_stage` 角色同一条校验路径），否则拼错的名字会一路滑到渲染。
- **有台词盖过、但没有角色出场的镜头（纯转场、纯 B-roll、纯图表）照样要写 `cast` 块**：`on_stage` 填空列表 `[]`，说话人不在台上即画外音；`pack_dir` 仍要指向本镜头目录内的角色包副本，画外音说话人那一份也要拷进来（校验按它确认说话人 id 没拼错）；`ground_y` 照常填（台上没人时它不进渲染提示词，但仍是必填字段，省掉会报 `cast.ground_y 必须在 (0,1) 区间内，收到 0`）。不写 `cast` 块会让 `am dialogue beats` 直接报错点名这个镜头；而为了糊弄校验把角色摆进 `on_stage`，渲染提示词就会声明「本镜头有角色出场」，角色被画到图表镜头上——两种都是错的。
- 整个时间段里一句台词都没有的镜头（例如纯音乐转场）不写 `cast` 块，与老镜头完全一样；`am dialogue beats` 不会碰这类镜头的 `scene.json`。
- 拆完全部镜头、跑完 `am dialogue beats` 之后执行 `am validate cast`：它校验班底与角色包是否自洽、每个角色是否声明了当前 `TTS_PROVIDER` 的音色、`production/dialogue.json` 是否完整覆盖字幕、以及全部镜头的 `cast.beats` 换算回全局时间后是否合并覆盖了 `dialogue.json` 的每一行——不通过不得开始渲染。

## 七、角色包的引入

- 角色包通过 `am cast add <外部角色包目录>` 引入一个已存在的角色，或 `am cast new <id>` 现场生成一个新角色骨架（`character.yaml` + `rig.svg` + `dna.md`）后手工补全真实 `voiceId`、外观与人设。两条命令都会自动把角色登记进项目根 `cast.yaml` 的 `packs` 列表，不需要手工编辑 `cast.yaml`。
- 登记是幂等的：重复引入同一路径不会在 `packs` 里留下重复项，`cast.yaml` 已有的 `defaults` 也原样保留。
- 写对话体脚本之前，先确认脚本里会用到的每一个说话人 id 都已经登记；未登记的说话人既过不了 `am cast validate`，也过不了渲染前的镜头级校验。
- 手改 `cast/<id>/character.yaml` 之后要重新跑一次 `am cast validate` 才会同步 `character.json`；渲染机实际读取的是 `character.json`，不会因为 yaml 改过就自动感知。
