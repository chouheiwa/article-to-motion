---
schema_version: 1
style_id: anime-cel-v1
style_name: 日系动漫
scope:
  - anime_episode_style_explainer
  - growth_and_challenge_stories
  - light_science_and_life_tips
  - youth_and_campus_topics
  - upbeat_concept_explainer
canvas:
  width_px: 1080
  height_px: 1920
  fps: 30
  orientation: vertical
safe_area:
  structural: {left_px: 40, right_px: 40, top_px: 96, bottom_px: 60}
  main_content: {left_px: 88, right_px: 88, top_px: 120, bottom_px: 100}
  critical_text: {left_px: 88, right_px: 180, top_px: 120, bottom_px: 260}
  cover_title: {left_px: 88, right_px: 180, top_px: 260, bottom_px: 940}
  subtitles: {left_px: 88, right_px: 180, top_px: 1470, bottom_px: 260}
colors:
  canvas: "#8FD3FF"
  sky_deep: "#2A6FC4"
  cloud: "#FFFFFF"
  cloud_shade: "#C9D6F2"
  paper: "#FFF8EC"
  ink: "#1B2240"
  ink_soft: "#4A5578"
  sakura: "#FF7FA8"
  sunbeam: "#FFD23F"
  impact_red: "#C42B3A"
  hope_green: "#2FB574"
typography:
  primary_stack: '"Noto Sans SC", sans-serif'
  mono_stack: '"Bebas Neue", "Noto Sans SC", sans-serif'
  display_stack: '"Smiley Sans", "Noto Sans SC", sans-serif'
  font_files:
    - {family: "Noto Sans SC", weight: 400, file: "assets/fonts/noto-sans-sc-400.woff2"}
    - {family: "Noto Sans SC", weight: 600, file: "assets/fonts/noto-sans-sc-600.woff2"}
    - {family: "Noto Sans SC", weight: 700, file: "assets/fonts/noto-sans-sc-700.woff2"}
    - {family: "Noto Sans SC", weight: 900, file: "assets/fonts/noto-sans-sc-900.woff2"}
    - {family: "Smiley Sans", weight: 400, file: "assets/fonts/smiley-sans-oblique.woff2"}
    - {family: "Bebas Neue", weight: 400, file: "assets/fonts/bebas-neue-400.woff2"}
  sizes_px:
    cover_min: 92
    cover_max: 132
    title_min: 54
    title_max: 76
    body_min: 26
    body_max: 36
    label_min: 22
    label_max: 28
  weights:
    display: 400
    heading: 400
    body: 400
    body_emphasis: 700
    metadata: 700
  line_heights:
    display_min: 1.00
    display_max: 1.10
    body: 1.42
    metadata: 1.15
spacing:
  content_left_px: 88
  content_width_px: 904
  group_min_px: 12
  group_max_px: 20
  card_stack_min_px: 16
  card_main_padding_min_px: 28
  card_main_padding_max_px: 40
  card_secondary_padding_min_px: 22
  card_secondary_padding_max_px: 28
  section_min_px: 32
  section_max_px: 60
  element_edge_min_px: 22
  skeleton_bottom_min_px: 22
radius:
  sm_px: 6
  md_px: 12
  lg_px: 20
  xl_px: 28
  pill_px: 999
scene_archetypes:
  - id: proposition
    name: 话数标题卡
    use_for: 封面、钩子、章节开场、一句话强结论
    example_png: assets/style-guide/examples/proposition.png
  - id: comparison
    name: 分格对照
    use_for: 两种做法、误区与真相、之前与之后、两种选择
    example_png: assets/style-guide/examples/comparison.png
  - id: process
    name: 分镜推进
    use_for: 步骤、因果链、成长阶段、按顺序发生的事
    example_png: assets/style-guide/examples/process.png
  - id: capability_deck
    name: 设定资料
    use_for: 多维要点、能力与特征、要素拆解、全片总结
    example_png: assets/style-guide/examples/capability_deck.png
motion:
  phases: {build: 0.28, breathe: 0.52, resolve: 0.20}
  entrance_seconds: {min: 0.20, max: 0.45}
  exit_seconds: {min: 0.15, max: 0.30}
  transition_seconds: {min: 0.20, max: 0.40}
  first_action_delay_seconds: {min: 0.10, max: 0.25}
  entrance_ease: [expo.out, power3.out, back.out(1.4)]
  exit_ease: [power2.in, expo.in]
  verbs: [TITLE_DROP, ZOOM_LINES, PANEL_IN, SMEAR, POP, SPARKLE, EYECATCH]
  ambient_per_scene_max: 1
  max_same_ease_tweens: 2
audio:
  sample_rate_hz: 48000
  channels: 2
  voice_integrated_lufs: {min: -18, max: -16}
  bgm_integrated_lufs: {min: -30, max: -26}
  bgm_ducking_db: {min: 6, max: 10}
  ducking_attack_seconds: {min: 0.04, max: 0.12}
  ducking_release_seconds: {min: 0.25, max: 0.60}
  bgm_fade_seconds: {min: 0.80, max: 1.50}
  sfx_gain_db: {min: -18, max: -12}
  sfx_max_simultaneous: 2
  final_integrated_lufs: -16
  final_lufs_tolerance: 1
  true_peak_max_dbtp: -1
  lra_lu: {min: 2, max: 6}
subtitles:
  required: false
  font_size_px: {min: 32, max: 38}
  max_width_px: 812
  max_lines: 2
  max_fullwidth_chars_per_line: 16
  line_height: {min: 1.30, max: 1.40}
  background: "rgba(27, 34, 64, 0.86)"
  text_color: "#FFFFFF"
  padding_px: {vertical: 14, horizontal: 22}
  radius_px: 12
  break_rules:
    - keep_proper_nouns_intact
    - keep_english_phrases_intact
    - keep_numbers_and_units_intact
    - preserve_real_speech_gaps
cover:
  frame_zero_complete: true
  default_lines: 2
  max_lines: 3
  max_fullwidth_chars_per_line: 8
  font_size_px: {min: 92, max: 132}
  line_height: {min: 1.00, max: 1.10}
  max_width_px: 812
  contrast_ratio_min: 7.0
  stable_frames: 18
forbidden:
  - black_or_blank_frame_zero
  - cover_fade_in_intermediate_state
  - copying_anime_ip_characters_logos_or_title_lettering
  - drawing_character_faces_or_imitating_specific_artists
  - photographic_or_ai_generated_anime_backgrounds
  - text_directly_on_sky_without_panel_or_ink_stroke
  - small_text_on_sakura_or_hope_green_other_than_ink
  - body_text_in_display_font
  - text_moving_while_being_read
  - zoom_lines_behind_body_text
  - full_screen_flash_strobe
  - continuous_or_long_screen_shake
  - gradient_text
  - colors_outside_palette_tokens
  - arbitrary_new_brand_colors
  - labels_or_icons_touching_edges
  - skeleton_or_list_without_bottom_padding
  - sfx_on_every_element
  - music_masking_voice
---

# 日系动漫

这是番剧感科普、成长故事与轻松讲解类竖屏 MG 视频的品牌层规范。YAML frontmatter 是唯一规范性 token；正文解释如何使用。

## 使用原则

- 品牌规范不是固定布局。根据文案语义选择话数标题卡、分格对照、分镜推进或设定资料镜头。
- 渲染工具负责镜头内部的道具、局部构图和动画编排，但不得改写核心颜色、字体角色、安全区、间距、圆角、动作语法和声音目标。
- 同一时刻只有一个主焦点；整镜最多两个焦点，按节拍依次登场，并包含背景（晴空与云）、中景（道具与分格）和前景（文字面板与贴纸）三个层次。
- 番剧语汇是承载信息的版式，不是装饰。集中线指向的必须是本拍的主焦点，分格的每一格必须对应文案里的一个真实步骤或概念。

## 品牌人格

明亮、热血、轻快、有一点中二。画面像一集 TV 动画正在播放：晴空与积云先铺好，话数和标题砸下来，集中线把视线收向关键词，读完之后画面稳稳停住。只借用番剧的共同语汇（话数标题卡、集中线、漫画分格、设定资料、拟声字、眼睛闪光式的星芒），**不得复刻任何具体作品的角色、Logo、标题字形或片头构图**。

## 赛璐珞画法

- 所有图形是平涂加硬边阴影：一个底色、一块同向的 `cloud_shade` 或 `ink` 低透明度硬阴影，不用柔和渐变表现体积。天空可以用 `sky_deep` → `canvas` 的竖向渐变，这是唯一允许的大面积渐变。
- 道具、面板、分格统一用 `ink` 描线，主体 5–6px、次级 3px；同一镜头线宽不超过两档。
- 光源统一在左上：阴影落在右下，高光是左上角一小块 `cloud` 平涂。
- 不画人物五官与角色立绘。需要「人」时只用无五官的剪影、手势或道具（书包、耳机、便当盒、纸飞机）暗示。

## 色彩与对比

- 颜色只取自 token，不临时引入新色。
- **文字不直接压在天空上。** 正文与标签只放在 `paper` / `cloud` 面板（配 `ink`）、`ink` 面板（配 `cloud` / `sunbeam`）或 `sunbeam` 贴纸（配 `ink`）上。
- 唯一例外是展示字的「描边字」：`cloud` 填充 + 至少 8px 的 `ink` 外描边（`paint-order: stroke`），对比度按填充色对描边色计算。描边字只用于 54px 以上的标题与拟声字。
- `sakura`、`hope_green`、`sunbeam` 是底色，不是文字色：上面只放 `ink` 文字。`impact_red` 可以做 `paper` 上的强调字，或配 `cloud` 文字的色块。
- `hope_green` 只表示「对 / 完成 / 好结局」，`impact_red` 只表示「错 / 危险 / 坏结局」，不作装饰轮换。

## 字体角色

- 展示字（封面、镜头标题、拟声字）：`Smiley Sans`（得意黑），即 `typography.display_stack`。它自带斜体，不要再加 `font-style: italic` 或 `skew`。
- 话数、编号、英文小标、`NEXT EPISODE` 这类元数据：`Bebas Neue`，即 `typography.mono_stack`。它只有大写拉丁字母与数字，中文会回退到 Noto Sans SC。
- 正文、说明、字幕（36px 以下的所有中文句子）：只用 `"Noto Sans SC", sans-serif`，400 / 700。展示字不得用于正文。中文正文不小于 24px。

## 构图

- 外层云、集中线、分格外框可以进入 `structural` 区；关键内容不得进入该区。
- 普通画面使用 `main_content` 区；标题、数字、结论使用 `critical_text` 区，主动避让抖音右侧互动栏和底部 UI。
- 集中线以主焦点为心，内圈留出不小于主焦点外接圆的空白；集中线不得穿过任何文字。
- 漫画分格之间的格间距 16–24px，格框 `ink` 5–6px；斜切分格的倾角统一在 6°–12°，同一镜头只用一个方向。

## 动画

遵循 Build / Breathe / Resolve。入场以 TITLE DROP、ZOOM LINES、PANEL IN、SMEAR、POP、SPARKLE、EYECATCH 为主要动作词：快速砸入、轻微过冲、硬停。退场使用 ease-in 快速切走。

- 番剧手感来自「关键姿势 + 停顿」：动作快进快停，停住后保持静止直到读完；不做持续漂浮、呼吸缩放或跑马灯。
- 图形道具（不含正文）可以用「一拍二」：动画时间按 `Math.floor(frame / 2) * 2` 取样，每两帧更新一次；文字入场不用一拍二，保持顺滑。
- SMEAR（运动模糊拖影）只给快速移动的图形，持续不超过 3 帧；文字不做拖影。
- 每镜头最多一种环境动作（云缓慢平移、集中线轻微轮换或星芒微闪，三选一），允许整段稳定停留。
- 禁止全屏闪白、频闪、持续震屏和弹簧/橡皮筋效果。震屏只用于关键落点，单次不超过 0.20s、位移不超过 10px。

动画必须确定性、可寻址，并可在任意帧重建。集中线的线条分布用固定种子预先算好，不得使用运行时随机数、墙钟时间或依赖播放顺序的状态。

## 字幕与封面

字幕是可选层。若内嵌字幕，严格使用 `safe_area.subtitles`、两行上限、`ink` 半透明圆角底和语义断行规则。

第 0 帧必须已经是完整封面（话数标题卡的完成态）。标题稳定至少 18 帧后才允许转场，不得以黑帧、空白或标题下落中的中间态开场。

## 声音

人声优先且保持原速。BGM 可以用明快的番剧风格编曲（吉他、弦乐、轻快鼓组），有人声时按 frontmatter 下压。SFX 只标记标题砸入、集中线、分格切换和结论，同时最多两个。

## 验收

1. 核心颜色、字体、圆角和间距是否来自 frontmatter。
2. `typography.font_files` 里用到的每个字重是否都有对应的 `@font-face`，`src` 是否指向镜头目录内真实存在的文件，是否都是 `font-display: block`。渲染机不装系统字体，漏一条就会静默回退成通用字体，本地却看不出来。
3. 有无文字直接压在天空上（描边字除外）；`sakura` / `hope_green` / `sunbeam` 上是否只有 `ink` 文字；正文是否只用 Noto Sans SC。
4. 集中线是否穿过文字；关键文字是否避让平台 UI；标签、编号距边缘是否至少 22px。
5. 第 0 帧是否完成、可读且稳定 18 帧。
6. 画面里是否出现任何动画作品的角色、人脸、Logo 或可辨认的标题字形。
