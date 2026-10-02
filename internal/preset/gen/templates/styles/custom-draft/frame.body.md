

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
4. 为 4 种骨架各画一张 {{CANVAS}} 的 SVG，放在 `assets/style-guide/examples/<id>.svg`，只用本风格声明的颜色与字体；运行 `am validate style --regenerate-examples` 渲染 PNG，再运行 `am validate style` 直到通过。
5. 最后把本文件正文改写成正式的使用说明（可参考说明书的结构），删掉本段草稿说明。

## 不能改的约束

- 第 0 帧必须是完整封面，标题稳定至少 18 帧（`cover.stable_frames`）。
- 动画必须确定性、可按任意帧重建，不得使用运行时随机数或墙钟时间。
- 关键文字放在 `critical_text` 区，主动避让{{PLATFORM_UI}}。
