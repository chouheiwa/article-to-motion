---
name: text-to-lottie
description: 为 HyperFrames 镜头制作 Lottie/Bodymovin JSON 图层资产——logo 演绎、图标、loader、状态反馈、SVG 描边绘制、UI 微交互、辉光玻璃金属一类矢量特效。当镜头需要一段时间线已经烘进资产里的矢量动画，或需要把一个 SVG 变成会动的元素时使用。
---

# Text to Lottie

在 HyperFrames 镜头里制作 Lottie 图层。交付物是**镜头里的一个元素**，不是一段孤立的 JSON，也不是整个镜头。

## 先判断该不该用 Lottie

默认不用。镜头的主体是 HyperFrames 组合的 HTML 与 GSAP 时间线，那条路径可控、可调、可被逐帧校对。只有当动画满足下面全部三条时才值得引入 Lottie：

1. 它是**矢量图形**的运动，不是版式或文字的运动；
2. 它的时间线**自成一体**，跟镜头的叙事节奏没有耦合，塞进去播完就行；
3. 用 CSS 或 GSAP 表达它会明显更笨重——典型是多段路径描边、复杂遮罩演变、几十个图形的编排。

符合的：logo 演绎、图标变形、loader、成功/失败一类状态反馈、SVG 描边绘制、UI 微交互、矢量质感特效。

**不符合的，一律不要用 Lottie 做：**

- 文字排版与逐字动画、标题入场、字幕——用 HTML 文本加 `hyperframes` 技能 `references/motion.md` 里的标题与文字手法。Lottie 里的文字要么被转成路径丢掉可访问性和清晰度，要么依赖字体嵌入，两条路在这个渲染链上都更差。
- 数字滚动、图表、数据统计——用 HTML 与 GSAP 驱动（`hyperframes` 技能 `references/runtimes.md` 写明图表由 GSAP tween 的 `onUpdate` 驱动），且数值经常要跟文案对齐，烘死在 JSON 里改不动。
- 转场、相机推拉、整镜头的运镜——这些是镜头级编排，归 HyperFrames 时间线管。
- 真实素材（照片、视频、截图）的处理。
- 整个镜头。**任何时候都不要产出一个铺满画幅、承担镜头全部内容的 Lottie。**

拿不准就不用 Lottie。用 HTML 做出来的东西后面能改，烘进 Lottie 的改不动。

## 读哪些参考

本文件是薄控制面。**只读与当前任务匹配的那一两份**，不要通读 `references/` 全部。

| 任务 | 读 |
|---|---|
| 任何新建或修改 Lottie 图层 | `references/lottie-spec-map.md` |
| logo 演绎 | `references/recipe-logo.md` + `references/motion-taste.md` |
| 图标、loader、spinner、状态反馈（成功/失败/警告/完成/空状态） | `references/recipe-loaders-icons.md` + `references/motion-taste.md` |
| 把一个 SVG 变成动画、描边绘制 | `references/recipe-svg-animation.md` + `references/svg-compatibility.md` |
| UI 微交互 | `references/recipe-ui-microinteractions.md` + `references/motion-taste.md` |
| 辉光、玻璃、金属、渐变、填充、爆开一类特效 | `references/recipe-visual-effects.md` + `references/motion-taste.md` |
| 输入是 SVG 的任何任务 | 对应配方 + `references/svg-compatibility.md` |
| 需求里出现「高级」「干净」「极简」「现代」「精致」一类形容词 | `references/design-taste.md` + 对应配方 |

`references/` 里是英文原文，出处与改动记录见 `ATTRIBUTION.md`。

## 产物与加载

**放置：** JSON 写在镜头目录内，例如 `assets/logo-reveal.json`（相对镜头目录）。不得写到镜头目录之外——渲染会拒绝逃出镜头目录的路径。

**加载：** 按 `hyperframes` 技能 `references/runtimes.md` 的 Lottie 一行与「dotLottie browser setup」一节接进组合。要点：

- 播放器是 **lottie-web**（或 dotLottie），不是 Skia Skottie。
- `autoplay: false`、`loop: false`，由 HyperFrames 统一 seek。
- 每个实例注册到 `window.__hfLottie`。
- 容器尺寸用 CSS 固定住。
- 资源从项目本地文件加载。

**格式：** 顶层写全 `v`、`fr`、`ip`、`op`、`w`、`h`、`nm`、`assets`、`layers`；`op` 是开区间。`fr` 与镜头帧率一致，避免重采样抖动。

## 与动效预算的边界

镜头提示词里那条动效预算——至少组合 3 条来自 3 个不同分类的 rule、至少再动用 2 个 `scale/x/y/opacity` 之外的属性、必须有一条持续性环境动效垫底、不得连续 45 帧完全静止——**由 HTML 侧独立满足**。

- Lottie 图层**不计入**任何一条 rule。放了一个 Lottie 不等于完成了动效预算。
- 持续性环境动效必须在 HTML 侧，**不能拿 Lottie 的循环顶替**。Lottie 播完就停，顶不住整个镜头时长。
- Lottie 图层按装饰层对待：显式标记 decorative，或落在语义元素的入场/退场窗口内，不得侵占可读稳定区间。

## 确定性

渲染是逐帧 seek 的，同一帧必须永远画出同样的像素。

- 不使用表达式、不读时钟、不用任何未播种的随机。粒子、轨道、噪声、物理一类系统必须**烘成关键帧**。
- 优先 shape layer。原生文字图层（`ty:5`）在这条链上依赖字体嵌入且各播放器行为不一，除非确有路径特效需求（描边绘制、字形形变），否则不要用；镜头里的文字交给 HTML。
- 避开各播放器支持度不一致的特性：表达式、混合模式、`ty:5` 文字槽、渲染器私有扩展。
- 慎用遮罩与相交路径，`references/svg-compatibility.md` 里列了具体的坑。

## 缓动

不要给所有图层套同一条缓动，也不要退回线性。按运动行为从 `references/motion-taste.md` 的锚点表里选，焦点元素的缓动最强。

## 交付前自查

1. JSON 能解析：`node -e "JSON.parse(require('fs').readFileSync('<路径>','utf8'))"`。
2. 走镜头的正常渲染流程出片，逐帧看首帧、中点、末帧：无空白、无缺失资源、无未上色图形、无图层顺序错乱、无内容被裁切。
3. 透明底还是实底是有意选的：logo、图标、loader、覆盖层默认透明，跟着 HTML 背景走。
4. 尺寸与画幅匹配，在镜头里不变形、不越界。
5. 动效预算由 HTML 侧满足，且末帧之后环境动效仍在跑。
6. 这个元素确实比用 HTML 做更合适——否则删掉它，改用 HTML。
