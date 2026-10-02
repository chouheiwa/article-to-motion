---
schema_version: 1
style_id: custom-draft
style_name: 自定义
scope:
  - custom_style_draft
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
  canvas: "#F5F7FB"
  ink: "#0E2340"
  engineering_blue: "#1857C4"
  capability_deck: "#12294A"
  blue_tint: "#EAF1FD"
  support_gray: "#4E6076"
  structure_line: "#D5DEEB"
  structure_line_strong: "#C3D2E6"
  white: "#FFFFFF"
  warning_red: "#A8412A"
  success_green: "#4FBF8B"
typography:
  primary_stack: '"Inter", "Noto Sans SC", sans-serif'
  mono_stack: '"JetBrains Mono", "Noto Sans SC", monospace'
  font_files:
    - {family: "Noto Sans SC", weight: 400, file: "assets/fonts/noto-sans-sc-400.woff2"}
    - {family: "Noto Sans SC", weight: 600, file: "assets/fonts/noto-sans-sc-600.woff2"}
    - {family: "Noto Sans SC", weight: 700, file: "assets/fonts/noto-sans-sc-700.woff2"}
    - {family: "Noto Sans SC", weight: 900, file: "assets/fonts/noto-sans-sc-900.woff2"}
  sizes_px:
    cover_min: 78
    cover_max: 112
    title_min: 46
    title_max: 64
    body_min: 24
    body_max: 34
    label_min: 18
    label_max: 23
  weights:
    display: 900
    heading: 700
    body: 400
    body_emphasis: 600
    metadata: 700
  line_heights:
    display_min: 0.98
    display_max: 1.08
    body: 1.35
    metadata: 1.2
spacing:
  content_left_px: 88
  content_width_px: 904
  group_min_px: 12
  group_max_px: 20
  card_stack_min_px: 16
  card_main_padding_min_px: 28
  card_main_padding_max_px: 36
  card_secondary_padding_min_px: 22
  card_secondary_padding_max_px: 28
  section_min_px: 32
  section_max_px: 56
  element_edge_min_px: 22
  skeleton_bottom_min_px: 22
radius:
  sm_px: 8
  md_px: 12
  lg_px: 18
  xl_px: 22
  pill_px: 999
scene_archetypes:
  - id: proposition
    name: 命题型
    use_for: 封面、钩子、章节开场、强结论
    example_png: assets/style-guide/examples/proposition.png
  - id: comparison
    name: 对照型
    use_for: 原料与结果、旧方法与新方法、条件与结论
    example_png: assets/style-guide/examples/comparison.png
  - id: process
    name: 流程型
    use_for: 步骤、层级、因果、状态变化
    example_png: assets/style-guide/examples/process.png
  - id: capability_deck
    name: 总览型
    use_for: 系统全貌、能力清单、要点汇总
    example_png: assets/style-guide/examples/capability_deck.png
motion:
  phases: {build: 0.30, breathe: 0.40, resolve: 0.30}
  entrance_seconds: {min: 0.30, max: 0.60}
  exit_seconds: {min: 0.18, max: 0.45}
  transition_seconds: {min: 0.20, max: 0.40}
  first_action_delay_seconds: {min: 0.10, max: 0.30}
  entrance_ease: [power2.out, power3.out, expo.out]
  exit_ease: [power2.in, power3.in]
  verbs: [DRAW, SLIDE, ASSEMBLE, FILL, LOCK_IN, STEP, VERIFY]
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
  background: "rgba(18, 41, 74, 0.84)"
  text_color: "#F5F7FB"
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
  max_fullwidth_chars_per_line: 10
  font_size_px: {min: 78, max: 112}
  line_height: {min: 0.98, max: 1.08}
  max_width_px: 812
  contrast_ratio_min: 7.0
  stable_frames: 18
forbidden:
  - black_or_blank_frame_zero
  - cover_fade_in_intermediate_state
  - arbitrary_new_brand_colors
  - labels_or_icons_touching_edges
  - skeleton_or_list_without_bottom_padding
  - sfx_on_every_element
  - music_masking_voice
---

# 自定义风格（草稿，尚未定稿）

本文件是自定义风格的草稿骨架，不是可以直接使用的视觉规范。项目按用户在 `docs/style-brief.md` 里的文字描述定制风格；在完成「定风格」阶段、`am validate style` 通过之前，任何镜头都不得渲染——`style_id` 仍为 `custom-draft` 时 `am scene run` 会直接拒绝。

## 定稿要做的事

1. 读 `docs/style-brief.md`，把描述落成具体决定：品牌人格、配色、字体角色、版式骨架、动效语法与禁用项。
2. 改写本文件的 frontmatter：
   - `style_id` 改成 `custom-<小写英文短名>`，`style_name` 改成这套风格的中文名，`scope` 写适用题材。
   - `canvas` 与 `safe_area` 来自画幅预设，**一个数字都不许改**。
   - `colors` 只写六位十六进制色；`typography` 的字体栈只能使用 `typography.font_files` 声明过的字体族或通用族名。字体池用 `am style fonts` 查看、`am style add-font` 拷进项目；也可以放入自带许可的 woff2 文件，但不得引用本机系统字体。
   - `scene_archetypes` 的 4 个 id 与顺序固定，只改 `name` 与 `use_for`。
   - 可选 `mood_refs`：氛围参照图路径列表，只用于质感与装饰语言。
3. 用同一份 frontmatter 改写 `docs/自定义-视频风格说明书.md`，两份 frontmatter 必须逐项相同；说明书正文写清风格定位、构图、动效、字幕与封面、声音和验收清单。
4. 为 4 种骨架各画一张 1080×1920 的 SVG，放在 `assets/style-guide/examples/<id>.svg`，只用本风格声明的颜色与字体；运行 `am validate style --regenerate-examples` 渲染 PNG，再运行 `am validate style` 直到通过。
5. 最后把本文件正文改写成正式的使用说明（可参考说明书的结构），删掉本段草稿说明。

## 不能改的约束

- 第 0 帧必须是完整封面，标题稳定至少 18 帧（`cover.stable_frames`）。
- 动画必须确定性、可按任意帧重建，不得使用运行时随机数或墙钟时间。
- 关键文字放在 `critical_text` 区，主动避让抖音右侧互动栏和底部 UI。
