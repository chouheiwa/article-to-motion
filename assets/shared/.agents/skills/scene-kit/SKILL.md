---
name: scene-kit
description: HyperFrames 镜头的按帧驱动工具库——缓动与淡入淡出、按短语帧取时间窗、按 frame.md 安全区取版式矩形、按 frame.md 字幕规范烧录字幕。写任何镜头合成时都可以用；镜头要内嵌字幕（frame.md 的 subtitles.required 为 true，或镜头本身需要烧录字幕）时必须用它，不得手写字幕代码。库文件是本目录下的 scene-kit.js。
---

# Scene Kit

每个镜头都要重新写一遍缓动函数、安全区换算和字幕，结果是镜头之间字幕位置、字号、断行各不相同，frame.md 里的字幕规范实际上没有任何共用代码去执行。`scene-kit.js` 把这些收成一个小库：**输入帧号，输出画面**，与 seek 顺序无关。

它不替你设计画面，也不建时间线。它只负责三件容易各写各的、写错了又不报错的事：时间换算、版式取值、字幕排版。

## 接进镜头

### 1. 把库复制进镜头目录

渲染只服务镜头目录内的文件，`<script src>` 指到镜头目录外会静默 404——字幕根本不出现，渲染却成功、退出码为 0：

```bash
cp <本技能目录>/scene-kit.js ./scene-kit.js
```

`<本技能目录>` 的本机绝对路径由 `am` 写在镜头提示词的「镜头工具库」一段里，直接用那一条，不要猜路径，不要引用镜头目录之外的文件。只复制 `scene-kit.js`；`example.html` 是写法参考，不要放进镜头目录（它会被当成第二个合成入口）。

### 2. 先加载 am-scene.js，再加载 scene-kit.js

`am-scene.js` 由 `am` 在渲染前写进镜头目录，定义 `window.AM_SCENE`：画布尺寸、帧率、总帧数、frame.md 的安全区与字幕规范、本镜头的字幕时间轴（`cues`）与短语时间窗（`phrases`）。分镜计划定了全片配色时还有 `look`（`background`、`ink`、`accent`，以及可选的 `surface`、`accent_2`，均为 `#RRGGBB`），颜色从这里读，不要在代码里另写一份；没定时这个键不存在。**不要自己写或改这个文件**，它每次渲染前都会被重新生成。

```html
<script src="https://cdn.jsdelivr.net/npm/gsap@3.14.2/dist/gsap.min.js"></script>
<script src="am-scene.js"></script>
<script src="scene-kit.js"></script>
```

没加载 `am-scene.js` 时，任何读数据的接口都会抛出「am-scene.js 未加载」。

### 3. 把 render(frame) 挂到时间线上

```js
var K = window.SceneKit;
var subs = K.captions(document.getElementById('root'));

function render(frame) {
  // 这里写所有「帧号 -> 画面」的取值
  subs.render(frame);
}

var tl = gsap.timeline({ paused: true });
// …其它 tween 照常挂在 tl 上…
K.attach(tl, render);
window.__timelines['main'] = tl; // 键名与根节点 data-composition-id 一致，建完再注册
```

完整写法见本目录的 `example.html`。

## API

所有帧号都是**镜头本地帧**：第 0 帧是本镜头第一帧，与 `cues` / `phrases` 的坐标系一致。

### 数学（不读 am-scene.js）

| 接口 | 说明 |
|---|---|
| `clamp(v, lo, hi)` | 钳位 |
| `lerp(a, b, t)` | 线性插值 |
| `ease(name, t)` / `ease(name)` | 求缓动值（t 先钳到 [0, 1]）/ 取缓动函数。可选：`linear`、`sineIn/Out/InOut`、`quadIn/Out/InOut`、`cubicIn/Out/InOut`、`expoIn/Out/InOut`、`backIn/Out/InOut`。未知名字抛错 |
| `progress(frame, start, end)` | frame 在区间里的进度，钳在 [0, 1] |
| `fade(frame, inStart, inEnd, outStart?, outEnd?)` | 淡入淡出系数；省略后两个参数表示不淡出 |
| `rng(seed)` | mulberry32 固定种子伪随机数，返回 `() => [0, 1)`。种子可以是数字或字符串 |

### 数据

| 接口 | 说明 |
|---|---|
| `scene()` | 返回 `window.AM_SCENE`；缺失或 schema 不符时抛错 |
| `frameOf(seconds)` | 时间轴秒数换本地帧号：`Math.round(秒 × fps)`。全库只有这一处换算 |
| `phrase(id)` | 短语时间窗 `{phrase_id, text, start_frame, end_frame_exclusive, min_readable_frames}`；未知 id 抛错并列出已有 id |
| `phraseActive(id, frame)` | `start_frame <= frame < end_frame_exclusive` |
| `cueAt(frame)` | 该帧所在的原始字幕条目（不含 lead/tail 放宽），重叠时开始得晚的胜出；没有则 `null` |

### 版式

`safeRect(name)` 返回 `{left, top, right, bottom, width, height}`，单位 px。`left/top/right/bottom` 是**距画布左上角的坐标**（`right = left + width`），不是 frame.md 里那种距各边的内缩量。`name` 取 frame.md `safe_area` 下的键，常见的有 `structural`、`main_content`、`critical_text`、`cover_title`、`subtitles`；未知名字抛错并列出已有键。

### 字幕

```js
var subs = SceneKit.captions(rootElement, options);
subs.render(frame);   // 返回当前显示的 cue 或 null
subs.element;         // 字幕层根元素
subs.layouts;         // 每条字幕的排版结果：{cue, fontSize, lines, overflow, from, until}
```

- **位置**：字幕层铺满 `safe_area.subtitles`，字幕框默认贴底居中。
- **样式**全部来自 `AM_SCENE.subtitles`：字号取 `font_size_px.max`，行高取 `line_height.max`，`background`、`text_color`、`padding_px`、`radius_px`、`max_width_px`（再与安全区宽度取小）。字体族继承自 `rootElement`，所以根节点上要设好 frame.md 规定的正文字体。
- **断行**：宽度按全角字计，汉字与全角标点算 1、ASCII 算 0.5，每行不超过 `max_fullwidth_chars_per_line`。不拆英文词、不拆数字与单位（`3.5 秒`、`98%`、`GPT-4`）、不拆《》“”""「」括起来的整段；优先在 `；，、。！？：` 之后断；两行时尽量等长；逗号句号不落行首。
- **超长**：放不进 `max_lines` 时逐像素缩小字号，最小到 `font_size_px.min`（每行可容纳字数按比例放宽，行的像素长度不变）；仍放不下才多出行数，并按字幕序号各 `console.warn` 一次。看到这条告警要回去把字幕拆短，而不是改库。
- **交接**：同一时刻只显示一条字幕，后一条一出现就截断前一条。
- **纯函数**：DOM 只在 `captions()` 时建一次；`render(frame)` 每次都把文字、字号、显隐全部写成该帧应有的值，任意帧、任意顺序调用结果相同。

| options | 默认 | 说明 |
|---|---|---|
| `fontSize` | `font_size_px.max` | 首选字号；缩字号的下限仍是 `font_size_px.min` |
| `lineHeight` | `line_height.max` | 行高倍数 |
| `fontFamily` / `fontWeight` | 继承 | 只在字幕要用与根节点不同的字体时设 |
| `leadFrames` / `tailFrames` | 0 | 让字幕提前出现 / 延后消失，但不侵入相邻字幕本来的显示区间 |
| `align` | `bottom` | 字幕框在安全区内的竖直位置：`bottom` / `center` / `top` |
| `safeArea` | `subtitles` | 换用别的安全区 |
| `cues` | `AM_SCENE.cues` | 换一组字幕时间轴（本地帧） |
| `className` / `zIndex` | — / 50 | 追加类名 / 层级 |

`AM_SCENE.subtitles` 为 `null`（frame.md 没有字幕规范）时 `captions()` 直接抛错，不凭空编一套样式。

### 接入时间线

`attach(timeline, render)` 把 `render(帧号)` 挂到一条 `gsap.timeline({ paused: true })` 上，立即出一次第 0 帧，返回一个无参 `draw()`（按时间线当前位置补出一帧）。帧号取 `Math.round(timeline.time() × fps)`，钳在 `[0, frames - 1]`。

只靠 `onUpdate` 不够：GSAP 的 `tl.seek(t)` 默认 `suppressEvents=true`，`onUpdate` 一次都不触发；宿主走 `window.__hf.seek` 时子时间线自己的 `seek` 也不会被调用。所以 `attach` 做三层（与上游 `hyperframes-animation/examples/messaging-multi-phrase.html` 和本项目 `character-rig` 的 `cast.js` 同一做法）：

1. 一条覆盖整镜的驱动 tween，`onUpdate` 里出帧；
2. 时间线自己的 `seek` / `pause` 包一层，调用后补出一帧；
3. `window.__hf` / `window.__player` 的 `seek` 包一层，补出所有已接入的帧。

自检时推进时间线一律用 `tl.time(t)` 或 `tl.seek(t, false)`。同页还用 `cast.js` 时，先 `await cast.mount(...)`，再调 `SceneKit.attach`。

## 确定性规则

- `render(frame)` 必须只由 `frame` 决定：不读时钟，不用未播种随机数（要随机就用 `rng(固定种子)`，并且在 render 之外一次取好），不在两帧之间累积状态，不靠「播过去了」翻标志位。
- 不用 `requestAnimationFrame`、定时器或 CSS `transition` / `@keyframes` 驱动画面；它们不受时间线管，成片要么静止要么乱。
- 不在 render 里创建或删除 DOM 节点，只改已有节点的属性与文字。
- 不手抄 frame.md 的像素与字号：版式用 `safeRect`，字幕用 `captions`，时间用 `phrase` / `cueAt`。frame.md 改了，手抄的那份不会跟着改。
- 第 0 帧必须已经是完整画面：`attach` 会立即画出第 0 帧，但入场动画写成「第 0 帧透明」的话照样是空封面。

`scene-kit.js` 自身通过 `am` 的确定性源码扫描；你在镜头里写的代码同样会被扫。
