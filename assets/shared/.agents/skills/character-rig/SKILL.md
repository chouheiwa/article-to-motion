---
name: character-rig
description: 在 HyperFrames 镜头里驱动角色包（character pack）——挂载角色、置姿势、转身、说话与倾听。当镜头的 scene.yaml 里有 cast 块、提示词出现「本镜头有角色出场」，或需要让一只简笔画角色在画面里做动作、转向、对话时使用。驱动库是本目录下的 cast.js。
---

# Character Rig

在 HyperFrames 镜头里驱动角色。交付物是**镜头里的角色元素与它挂在时间线上的那些 tween**，不是一套独立的动画系统。

角色包本身不由你创作：它由 `am cast new/add` 生成并校验，人工验收过。你的工作只有一件——把它正确地挂进镜头，并在那条 paused timeline 上编排它的动作。

## 四条硬规则

这四条不是风格建议。违反任何一条，本地预览完全正常、渲染退出码为 0，而成片是错的——这是这个项目最贵的失败模式，因为没有任何信号告诉你出了问题。

### 1. 所有 tween 必须挂在调用方那条 paused timeline 上

HyperFrames 是单条 paused timeline + 逐帧 seek 出帧。不受那条 timeline 管的动画在出帧时压根不推进（或按墙钟时间乱推进），成片要么完全静止，要么每帧随机。

- `cast.js` 自己不建 timeline、不调 `requestAnimationFrame`、不用定时器。
- 你也不许。不要给 rig 写 CSS `@keyframes`/`transition`，不要在 rig.svg 里放 `<animate>`（`am cast validate` 会拦，但拼进 HTML 的那部分没人拦你）。
- `to`/`turn`/`flip`/`speak`/`listen` 的第一个参数都是那条 timeline，传错会立刻抛错——这是故意的。

**推进时间线只能用 `tl.time(t)` 或 `tl.seek(t, false)`，不要用 `tl.seek(t)`。** GSAP 的
`seek(position, suppressEvents)` 里 `suppressEvents` **默认是 true**，`onUpdate` 一次都不会触发。
实测同一条时间线：

| 调用 | 结果 |
|---|---|
| `tl.seek(3.0)` | onUpdate 不触发 |
| `tl.seek(3.0, false)` | 正常出帧 |
| `tl.time(3.0)` / `tl.progress(p)` | 正常出帧 |

`cast.js` 为此做了两层保险（照 HyperFrames 0.8.14 的上游官方示例 `hyperframes-animation/examples/messaging-multi-phrase.html`，该目录 0.8.185 起已被上游并掉）：
一条覆盖全片的驱动 tween，加上对 `window.__hf` / `window.__player` 的 `seek` 与 timeline 自身
`seek` 的转发包装。所以即使宿主用默认参数 seek，角色照样出帧。**但你自己写的 tween 没有这层保险**
——预览与自检一律用 `tl.time(t)`。

### 2. 每个 onUpdate 只能是「当前进度 → 画面」的纯函数

渲染机按任意帧 seek，**不保证顺序、不保证只走一遍**。任何「播过去了所以状态变了」的实现（在 `onUpdate` 里翻标志位、做累积、读上一帧）都会给出乱掉的帧，而本地顺序播放看不出来。

```js
// 对：视图与压扁量每次求值只依赖当前进度
onUpdate: () => { const p = proxy.t; applyView(seq[Math.round(p * segments)], Math.abs(Math.cos(Math.PI * p * segments))); }

// 错：靠"走到这儿了"改状态
onUpdate: () => { if (proxy.t > 0.5 && !flipped) { flipped = true; showSide(); } }
```

`cast.js` 内部已经这么写了。你自己加的每一条 tween 也必须这么写。

### 3. 所有视图在 mount 时一次性挂进 DOM 并预置隐藏

转身只切换可见性。不能在转身那一刻现取现挂：逐帧 seek 下异步加载的时机不可控，会抓到还没挂上的空帧——而且只有部分帧空，抽检很容易漏掉。

`cast.mount()` 已经做完了这件事（它是整条链路上唯一的 `await` 点）。**不要**在时间线回调里 `fetch`、`import`、插入新的 SVG。

### 4. 第 0 帧必须已经是终态

`pose()` 是同步置位，`mount()` 返回前就已经调用过一次（默认 `idle`，或 `opts.pose`）。首帧不得留空台、不得停在入场中间态——抖音封面取的就是第 0 帧，这个项目在这里踩过一次。

如果角色需要「入场」，让它从画面外滑进来是**动作**，不是「还没加载好」：第 0 帧它必须已经是完整的、姿势正确的角色，只是位置在画外。

## 角色包长什么样

角色包在**镜头目录内**（`scene.yaml` 的 `cast.pack_dir`，通常就是 `cast/`），每个角色一个子目录：

```
cast/
  heiwa/
    character.yaml    人工编辑的唯一真相源（你不改它）
    character.json    ← cast.js 读的就是这个
    rig.svg           默认视图（front）的骨架图
    rig-side.svg      可选：其它视图
    dna.md            角色设定，供台词与音色参考
```

**`cast.js` 只读 `character.json`，绝不在浏览器里解析 YAML。** 渲染机是干净的无头 Chrome，CSP 下引不进 YAML 库，手写 YAML 子集解析器纯属埋雷。`character.json` 由 `am cast new/add/validate` 每次从 `character.yaml` 重新生成，键名是小驼峰：

```json
{
  "schema": "cast/v1",
  "id": "heiwa",
  "name": "黑娃",
  "voice": { "minimax": { "voiceId": "…", "speed": 0.98 } },
  "rig": {
    "file": "rig.svg",
    "viewBox": [0, 0, 400, 520],
    "baselineY": 512,
    "joints": { "head": { "pivot": [200, 168], "rotate": [-18, 18] } }
  },
  "views": { "side": { "file": "rig-side.svg", "viewBox": [0, 0, 400, 520], "baselineY": 512, "joints": { "head": { "pivot": [186, 170], "rotate": [-22, 22] } } } },
  "turns": { "front->side": { "via": ["three-quarter"], "durationMs": 520 } },
  "scale": { "heightRatio": [0.22, 0.32] },
  "poses": { "idle": {}, "pointing": { "frontLeg": 48, "head": -6 } }
}
```

要点：

- `rig` 就是默认视图，它的视图名是 **`front`**，不出现在 `views` 里。
- 关节在 `rig.svg` 里是 `<g id="j-<关节名">`，支点 `pivot` 用 viewBox 的 user unit 绝对坐标。
- `poses` 里的角度跨视图共享；每个视图各自声明自己的 `pivot` 与 `rotate` 区间。
- `heightRatio` 是角色高度占画面高度的比例区间（典型 0.22–0.32），`baselineY` 是脚底线。

## 接进镜头

把 `cast.js` 从技能目录拷进镜头目录，用相对路径引入——渲染只服务镜头目录内的文件，`<script src>` 指到镜头目录外会静默 404（角色根本不出现，渲染却成功、退出码为 0）：

```bash
cp <本技能目录>/cast.js ./cast.js
```

`<本技能目录>` 的本机绝对路径由 `am` 写在镜头提示词的「角色驱动（按需）」一段里，直接用那一条，不要猜路径、不要引用镜头目录之外的文件。这条复制义务同时写在提示词的「角色（强制）」段里，是硬要求。

```html
<script src="cast.js"></script>
```

然后：

```js
const tl = gsap.timeline({ paused: true });   // 镜头那条唯一的 timeline

const heiwa = await cast.mount('#stage', {
  pack: 'cast/heiwa',   // 相对镜头 HTML 的角色包目录
  x: 0.34,              // 水平位置，画面宽度归一化比例，锚在角色左右中线
  ground: 0.78,         // 地平线，画面高度归一化比例，脚底 baselineY 对齐它
  facing: 'right',      // 'right'（默认）| 'left'
  view: 'front',        // 初始视图，默认 front
  pose: 'idle',         // 初始姿势，默认 idle
  duration: 6.0,        // 镜头总时长（秒）。给了它，驱动 tween 从第 0 秒覆盖到片尾，
                        // 每一帧都重算角色；不给则只覆盖到最后一个角色事件结束。
});

heiwa.pose('pointing');                                    // 同步置位，用于第 0 帧
heiwa.to(tl, 'doubting', { at: 1.2, dur: 0.4, ease: 'power2.out' });
heiwa.turn(tl, 'side', { at: 2.0 });                       // 时长取 turns 声明
heiwa.speak(tl, { from: 0.00, to: 4.32 });                 // 台词节拍来自提示词
heiwa.listen(tl, { from: 4.52, to: 5.60 });
```

`mount` 是 `async` 的，**必须 await 完再建后面的 tween**；舞台元素必须已经有布局尺寸（`clientWidth/clientHeight` 非 0），否则会抛错而不是静默摆错位置。

参数一律校验后再用，不做静默 clamp、也不回退默认值——位置写错要在第一次跑的时候响，而不是等看
成片时才发现角色贴在画边（`heightRatio` 为负更隐蔽：它会算出负的缩放，角色上下翻转还缩到画外）。
三个数值参数的区间不完全相同，因为含义不同：

| 参数 | 合法区间 | 为什么 |
|---|---|---|
| `x` / `ground` | 闭区间 `[0, 1]` | 0 和 1 就是画面边缘，把角色贴边是合法构图 |
| `heightRatio` | 开区间 `(0, 1)` | 与 `character.yaml` 的 `scale.heightRatio` 同口径（`am cast validate` 要求 `0 < lo < hi < 1`）；0 是没有高度，1 是顶天立地 |

`facing` 不是 `'left'` / `'right'`、姿势或视图名没声明过，同样立刻抛错。

### 尺寸与站位怎么算的

- 角色绘制高度 = 舞台高度 × `heightRatio` 中值（可用 `opts.heightRatio` 覆盖）。
- 角色左右中线对齐 `x`，脚底（`baselineY`）对齐 `ground`。
- 每个视图各自按自己的 `viewBox`/`baselineY` 定位，所以三视图的脚底在同一条线上，转身时不会上下跳。
- 想让角色更近/更远，调 `heightRatio` 而不是给容器套 `transform: scale()`——套 scale 会连描边一起放大缩小，这批角色的描边粗细是设计过的。

## 姿势

| 方法 | 用途 |
|---|---|
| `pose(name)` | **同步**置位。第 0 帧、或任何「不需要过渡」的地方用它 |
| `to(tl, name, { at, dur, ease })` | 在时间线上把姿势 tween 过去 |

两者都**对所有视图同时置位**。这一点很重要：姿势是跨视图共享的，只置当前视图的话，角色一转身姿势就掉回 idle——而且是转身之后才暴露，前面的帧都对。

`pose`/`to` 只认 `character.json` 里声明过的姿势名，写错立刻抛错，不会静默退回 idle。缺席的关节按 0 处理。合成后的角度会被夹进该视图 `rotate` 区间内。

## 朝向、视图，与它们的正交性

**`facing`（左右镜像）与 `view`（转过去多少）是两个独立维度。** 一个朝左的角色仍然可以是正面视图（面对镜头但站位偏左），一个侧面视图的角色也可以朝左或朝右。

- `facing: 'left'` 用 `scaleX(-1)` 实现。
- 带 `data-no-mirror` 的元素会被自动二次翻转（`scaleX(-1)` + `transform-box: fill-box`），否则角色举牌镜头会出反字。牌子、字幕板、任何带文字或方向语义的元素都要在 rig.svg 里打上 `data-no-mirror`（标在元素自己或它的任一祖先上都算）。
  **`<text>` / `<tspan>` 漏标 `am cast validate` 会拦**（与 `<animate>` 同一道校验，直接判角色包不合格）。但它只认这两个标签：箭头、指向性图标这类**没有文字的方向语义元素查不出来**，漏标不会有任何提示，只会在 `facing: left` 的镜头里反过来——这类元素只能靠你自己标。
- 需要在时间线上改朝向用 `flip(tl, { at, dur })`，它和 `turn` 一样是压扁过零、绝不是把 `scaleX` 直接 tween 到 -1 后就完事。

## 转身

### 转身不是把镜像 tween 过去

直接把 `scaleX` 从 1 tween 到 -1，在宽度过零时角色会翻成**反面**——读起来是卡片翻面，不是一个活物转身。

正确做法（`turn()` 已经实现）：`scaleX` 压到 0 再回到 1，**在过零那一帧换视图**，中间按 `turns[key].via` 经过四分之三侧。

压扁的零点与换视图的时刻必须严格对齐。错开一点点，就会在角色还有可见宽度的时候换图，肉眼看是「闪了一下换了张图」而不是转身。`cast.js` 里两者用同一个 `phase` 算出来，别自己另写一套。

### `views` 与 `turns` 怎么声明

在 `character.yaml` 里（`character.json` 自动跟上）：

```yaml
views:
  three-quarter: { file: rig-tq.svg,   viewBox: [0,0,400,520], baselineY: 512, joints: { … } }
  side:          { file: rig-side.svg, viewBox: [0,0,400,520], baselineY: 512, joints: { … } }
turns:
  "front->side": { via: [three-quarter], durationMs: 520 }
  "side->front": { via: [three-quarter], durationMs: 520 }
```

- 键必须写成 `<from>-><to>`，`from`/`to`/`via` 引用的视图都得先声明过。
- **未声明的转身组合直接抛错，不做自动寻路。** 哪条路径好看是创作判断，不是图论问题：`front->back` 是从左边转还是右边转，画面观感完全不同，只能由人写死。
- 想双向转身就写两条，`am cast validate` 不会替你补。

**`turn()` 与 `flip()` 必须按时间先后声明，且区间不得重叠**，否则会抛错。两条理由：

- 这两个方法要用「上一次转到哪儿」来算 `turns` 的键和起始朝向。先写 `at: 5.0` 再写 `at: 2.0`，
  后者拿到的是未来的状态当起点，两条都算错。
- 区间重叠时，后一条从它的 `at` 那一刻起整个盖住前一条：前一次转身的后半段被静默丢弃，而后
  一条在自己的进度 0 处是**满宽**的——画面上就是角色还有可见宽度时硬切了视图，正是上面
  「转身不是把镜像 tween 过去」那条要防的事。`turn(at: 2.0)`（520ms）后面紧跟
  `flip(at: 2.2)` 就会这样，所以它会被拒绝；下一次转身最早只能从 2.52 开始。

`pose` / `to` / `speak` / `listen` 不动这个游标，可以任意顺序穿插，也可以和转身区间重叠
（说话时转身是正常的表演）。

### 三视图是可选的：先问值不值

`views` 和 `turns` 都是**可选**的。只画了默认视图的角色包完全合法，`turn()` 在这种包上会退化成**镜像翻转**（等价于 `flip()`）：同样是压扁过零，但换的是朝向而不是视图。对大多数角色，这就够了。

画全三视图的准入成本是**三倍**：三张 rig.svg、三套关节支点、每个姿势都要在三个视图里都落在各自的 `rotate` 区间内（`am cast validate` 会逐视图检查，一处不合就整包不过）。

所以：**只有真的有转身戏份的主角才值得画全三视图。** 配角、只出现一两个镜头的角色、全程面向镜头的角色，画一个 `front` 就够了；需要它「转过去」的时候用退化的镜像翻转，观众读得懂。

## 说话与倾听

```js
heiwa.speak(tl,  { from: 0.00, to: 4.32 });   // 台词节拍由提示词给出，单位是镜头本地时间（秒）
heiwa.listen(tl, { from: 4.52, to: 5.60 });
```

**刻意不做口型音素同步。** 这个画风是面无表情的简笔画，加嘴型会毁掉它——一旦嘴动起来，观众就会开始对口型，而简笔画永远对不准，整个角色瞬间变廉价。

「在说话」由极轻微的头部起伏、呼吸和尾巴表达。`speak` 与 `listen` 用的是同一套运动，**区别只有幅度**（listen 约为 speak 的三分之一）。这也正是真实对话里的样子：听的人也在动，只是动得小。

细节：起伏是时间的正弦函数，在区间两端有淡入淡出包络，所以 seek 到区间之外时叠加量恰好为 0，不会有残留角度粘在身上。呼吸与姿势分开算、合成后写一次 `transform`，和 `to()` 的姿势 tween 不会互相覆盖——两者顺序随便排都不打架。

**关节命名影响呼吸挑谁。** 驱动库按名字匹配：含 `head` / `neck` 的关节做小幅点头，含 `tail` 的做
大幅摆尾。两类都没有时退回「第一个关节做最小幅度起伏」的兜底——不会报错，但角色会显得偏死。
画 rig 时把头关节命名成 `head`（或 `neckHead` 一类含 `head` 的名字）就能拿到应有的表演。

## 自检接口

```js
tl.time(2.05);               // ← 出帧只有这一条路：推时间线
heiwa.stageAt(2.05);         // => { view: 'front', sign: 0 }，纯取值，不碰 DOM
cast.pure.pinchScaleAtPhase  // 压扁曲线等取值函数，可直接单独求值
```

**出某一帧必须推时间线（`tl.time(t)`），不能用 `renderAt(t)` 代替。**

`renderAt` 重算的是视图、朝向和呼吸——这三样是时间的纯函数。**姿势不在其中**：关节角由
时间线上的 GSAP tween 拥有，`renderAt` 只是把「当前的」角度拿来合成。所以
`tl.time(0)` 之后再 `renderAt(2.0)`，你会得到 t=2.0 的视图配 t=0 的姿势——一张现实中
不存在的帧。这个接口存在是为了给宿主 seek 之后补一次渲染，不是给人当出帧入口用的；
不同步时它会直接抛错，而不是画出那张混合帧。

`stageAt(t)` 是纯取值，问任何 t 都安全，适合断言「第几帧该显示哪个视图」。

## 常见的坑

- **多视图的 SVG id 撞车。** 三张 rig 往往是同一张图改出来的，渐变/遮罩/滤镜的 id 大概率重名；三份 SVG 同时在文档里时，`url(#grad)` 一律解析到文档中第一个匹配元素。`cast.js` 会在挂载时给每个视图的 id 加 `cast-<id>-<view>-` 前缀并同步改写引用——所以**不要用 `#j-head` 这类选择器从 CSS 或 JS 直接命中 rig 内部元素**，走 handle 提供的接口。
- **rig.svg 里的 `<style>` 会泄漏到别的视图。** inline SVG 的 `<style>` 在 HTML 文档里是全局
  作用域，三份同源改出来的 rig 共用 `.c` 或 `circle` 这类选择器几乎是必然，最后挂上的那份会盖住
  全部三个视图。`cast.js` 在挂载时给每条规则加上 `[data-cast-scope="<id>-<view>"]` 前缀来隔离。

  这个改写是按「选择器 `{`」切分的正则，不是 CSS 解析器。两种最容易随手写出来的
  输入被显式拦下，遇到会直接抛错（而不是悄悄改坏）：

  | 拦下的写法 | 为什么 | 改成 |
  |---|---|---|
  | CSS 注释 `/* … */` | 注释里的花括号会被当成规则边界，整段样式报废 | 删掉 |
  | 选择器里的引号，如 `[title="a,b"]` | 按逗号拆分选择器时会把引号里的逗号也拆开 | 用 class |

  **这张表不是穷举，正则也拦不全。** 清单之外的怪异选择器仍可能被静默改坏——例如
  选择器里出现 `@`（`[data-x@y]`、转义写法 `.a\@b`）会撞上正则里排除 at-rule 的那一段，
  规则被整条跳过、作用域前缀没加上，于是它照旧泄漏到别的视图，而且**不抛错**。
  所以：rig 的样式请保持极简，能用属性（`fill="…"`）就别用 `<style>`，非用不可就只写
  最朴素的 class 与元素选择器。样式没生效或串到别的视图去时，先怀疑这里。
- **SVG 是 XML，属性必须带值。** `<text data-no-mirror>` 在 HTML 里合法，在 SVG 里是解析错误，
  `cast.js` 会直接抛「不是合法 SVG」。写成 `data-no-mirror="true"`。
- **舞台没有定位上下文。** `cast.mount` 会在舞台是 `position: static` 时把它改成 `relative`；但如果舞台本身尺寸是 0（还没布局、或用了 `display: contents`），会直接抛错。
- **在 `mount` 之前建 tween。** `mount` 是异步的，`await` 之前拿不到 handle。
- **给整个角色套 CSS 动画来"补一点动感"。** 见硬规则 1，成片会错。要动感就往那条 timeline 上加 tween。

## 交付前自查

1. 页面控制台无报错——`cast.js` 的错误都是显式抛出的，静默失败在这条链路上比崩溃贵得多。
2. 第 0 帧截图：角色在位、姿势是终态、没有半透明或空台。
3. 有转身的镜头，抓压扁过零附近的三帧（前一帧、零点帧、后一帧）：宽度连续、换图发生在最窄那一帧、脚底不跳。
4. 乱序求值一致性（硬规则 2 的实际验收方式）。这条必须写成**能失败**的形式，否则等于没查：

   ```js
   const t = [0.4, 2.05, 3.1, 4.7, 5.9];
   const forward = t.map((x) => { tl.time(x); return heiwa.body.style.transform + JSON.stringify(heiwa.stageAt(x)); });
   const shuffled = [...t].reverse().map((x) => { tl.time(x); return heiwa.body.style.transform + JSON.stringify(heiwa.stageAt(x)); });
   console.assert(forward.join() === [...shuffled].reverse().join(), '乱序 seek 出现错帧');
   console.assert(new Set(forward).size > 1, '所有帧都一样：时间根本没推进，这条自检是空转');
   ```

   第二条断言不能省。用 `tl.seek(t)`（默认参数）时 `onUpdate` 不触发，5 帧会是同一张静止图，
   第一条断言照样通过——自查空转、什么也没查出来。

   同理不要用 `heiwa.renderAt(t)` 采样：它不回算姿势，采出来的是「这一刻的视图配上一刻的
   姿势」。真这么写的话它会抛错，但别指望这个——直接用 `tl.time(t)`。
5. 台词区间内角色有可见但克制的生命感；区间外回到静止，没有残留的歪头。
6. 角色高度落在 `heightRatio` 区间内，脚底压在 `ground` 上，没有穿地或悬空。
