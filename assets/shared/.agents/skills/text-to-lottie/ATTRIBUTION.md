# 出处与改动记录

`references/` 下的九份文档 fork 自开源项目 [diffusionstudio/lottie](https://github.com/diffusionstudio/lottie)，MIT 许可，版权声明保留在同目录 `LICENSE`。

- 上游 commit：`3c72912fad543897f90045ed4d355813837927fc`（2026-07-25）
- 上游路径：`skills/text-to-lottie/references/`

## 保留的文件

| 文件 | 作用 |
|---|---|
| `design-taste.md` | 克制、留白、层级的设计默认值 |
| `motion-taste.md` | 按运动行为选缓动的锚点表 |
| `lottie-spec-map.md` | Lottie JSON 结构速查 |
| `svg-compatibility.md` | SVG 转 Lottie 的兼容性陷阱 |
| `recipe-logo.md` | logo 演绎 |
| `recipe-loaders-icons.md` | loader、图标、状态反馈 |
| `recipe-svg-animation.md` | 通用 SVG 动画与描边绘制 |
| `recipe-ui-microinteractions.md` | UI 微交互 |
| `recipe-visual-effects.md` | 辉光、玻璃、金属、渐变 |

## 未保留的文件

- `player-contract.md` —— 整份是上游那个 Vite + Skia Skottie 播放器项目的契约（场景放在 `public/projects/<project>/<scene-N>/`、起 dev server、读 `GET /__context`、浏览器 `?frame=N` 逐帧看）。本项目的镜头是 HyperFrames 组合，由 `am scene run` 无头渲染，这套契约整体不适用。
- `evals/` —— 上游维护它自己那份 SKILL.md 的回归用例，对使用者无意义。
- `recipe-typography.md`、`recipe-data-stats.md`、`recipe-product-promo.md`、`recipe-camera-scene-motion.md`、`recipe-diagram-technical.md`、`recipe-lower-thirds.md`、`chapterization-transition-grammar.md`、`recipe-starter-projects.md` —— 这些是「整个镜头就是一个 Lottie」尺度的配方。本项目把 Lottie 定位成镜头里的元素，整镜头的文字排版、数据统计、转场语法由 HyperFrames 官方技能负责（0.8.14 时是 `hyperframes-animation` 的 rule 体系，0.8.185 起并进 `hyperframes` 的 `references/motion.md`），两边都写会给出互相冲突的指令。

## 对保留文件的改动

只改了播放器耦合，共 11 处，全部是「在 Skottie 里验证」一类的句子，改指 lottie-web 与 HyperFrames 渲染：

| 文件 | 处数 |
|---|---|
| `svg-compatibility.md` | 4 |
| `motion-taste.md` | 2 |
| `recipe-visual-effects.md` | 2 |
| `lottie-spec-map.md` | 1 |
| `recipe-logo.md` | 1 |
| `recipe-svg-animation.md` | 1 |

正文其余部分保持上游英文原文未动，便于日后与上游 diff 同步。同步上游时按本表重做这 11 处替换即可。

`SKILL.md` 不是 fork，是本项目自行编写的。
