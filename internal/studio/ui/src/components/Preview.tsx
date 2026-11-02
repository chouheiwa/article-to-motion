import { useEffect, useRef, useState } from 'preact/hooks';
import { api, errorText, rawURL } from '../api';
import type { Attachment, BeatState, Box, ProjectState, Scene, TreeFile } from '../types';
import { cssSelector } from '../selector';

export type Tab = 'scene' | 'compose' | 'final' | 'style' | 'review' | 'publish' | 'file';

const tabs: [Tab, string][] = [
  ['scene', '镜头'],
  ['compose', '合成'],
  ['final', '成片'],
  ['style', '风格'],
  ['review', '复查'],
  ['publish', '发布'],
  ['file', '文件'],
];

/** 按钮触发的写任务：返回 task_id 的接口。App 负责发请求、提示与刷新任务列表。 */
export type RunAction = (path: string, payload?: unknown) => void;

interface Props {
  tab: Tab;
  onTab: (tab: Tab) => void;
  state: ProjectState | null;
  scenes: Scene[];
  sceneId: string;
  filePath: string;
  tree: TreeFile[];
  version: number;
  beat: BeatState | null;
  busy: string;
  onAttach: (attachment: Attachment) => void;
  onFile: (path: string) => void;
  run: RunAction;
}

export function Preview(props: Props) {
  const scene = props.scenes.find((s) => s.id === props.sceneId) ?? null;
  return (
    <main class="pane center">
      <nav class="tabs" role="tablist">
        {tabs.map(([id, label]) => (
          <button key={id} role="tab" class={props.tab === id ? 'on' : ''} data-tab={id} onClick={() => props.onTab(id)}>
            {label}
          </button>
        ))}
      </nav>
      {props.beat?.enabled && <BeatBanner beat={props.beat} version={props.version} run={props.run} />}
      <div class="preview-body">
        {props.tab === 'scene' && <SceneTab {...props} scene={scene} />}
        {props.tab === 'compose' && <ComposeTab {...props} scene={scene} />}
        {props.tab === 'final' && <FinalTab tree={props.tree} version={props.version} run={props.run} />}
        {props.tab === 'style' && <StyleTab tree={props.tree} version={props.version} onFile={props.onFile} />}
        {props.tab === 'review' && <ReviewTab tree={props.tree} version={props.version} run={props.run} />}
        {props.tab === 'publish' && <PublishTab version={props.version} run={props.run} />}
        {props.tab === 'file' && <FileView path={props.filePath} version={props.version} />}
      </div>
    </main>
  );
}

function Empty({ children }: { children: preact.ComponentChildren }) {
  return <div class="empty">{children}</div>;
}

function BeatBanner({ beat, version, run }: { beat: BeatState; version: number; run: RunAction }) {
  if (beat.confirmed) return <div class="banner ok">卡点网格已确认。</div>;
  if (!beat.click) return <div class="banner">卡点模式：还没有试听轨，先在对话里让它跑 am beat analyze。</div>;
  return (
    <div class="banner beat" id="beat-banner">
      <span>试听草稿网格：每拍一声，重拍更高。重拍对不上就在对话里说，确认无误再导入。</span>
      <audio controls preload="none" src={rawURL(beat.click_path, version)} />
      <button onClick={() => run('/api/commands/beat.confirm')}>确认网格</button>
    </div>
  );
}

const FPS = 30;

function SceneTab(props: Props & { scene: Scene | null }) {
  const { scene } = props;
  const video = useRef<HTMLVideoElement>(null);
  const [time, setTime] = useState(0);
  const [selecting, setSelecting] = useState(false);
  const [drag, setDrag] = useState<{ x0: number; y0: number; x1: number; y1: number } | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    setDrag(null);
    setSelecting(false);
    setError('');
  }, [scene?.id]);

  if (!scene) return <Empty>在左边选一个镜头。</Empty>;

  const step = (frames: number) => {
    const v = video.current;
    if (!v) return;
    v.pause();
    v.currentTime = Math.max(0, Math.min(v.duration || 0, v.currentTime + frames / FPS));
  };

  // 视频按 object-fit: contain 显示，算出画面实际占的矩形，才能把鼠标坐标换成视频像素。
  const toVideoBox = (): Box | undefined => {
    const v = video.current;
    if (!v || !drag || !v.videoWidth) return undefined;
    const scale = Math.min(v.clientWidth / v.videoWidth, v.clientHeight / v.videoHeight);
    const offsetX = (v.clientWidth - v.videoWidth * scale) / 2;
    const offsetY = (v.clientHeight - v.videoHeight * scale) / 2;
    const clamp = (value: number, limit: number) => Math.max(0, Math.min(limit, Math.round(value)));
    const x0 = clamp((Math.min(drag.x0, drag.x1) - offsetX) / scale, v.videoWidth);
    const y0 = clamp((Math.min(drag.y0, drag.y1) - offsetY) / scale, v.videoHeight);
    const x1 = clamp((Math.max(drag.x0, drag.x1) - offsetX) / scale, v.videoWidth);
    const y1 = clamp((Math.max(drag.y0, drag.y1) - offsetY) / scale, v.videoHeight);
    if (x1 - x0 < 4 || y1 - y0 < 4) return undefined;
    return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
  };

  const attach = async () => {
    const v = video.current;
    if (!v) return;
    setError('');
    try {
      const attachment = await api<Attachment>(`/api/scenes/${encodeURIComponent(scene.id)}/attachment`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ seconds: Number(v.currentTime.toFixed(3)), region: toVideoBox() }),
      });
      props.onAttach(attachment);
      setDrag(null);
      setSelecting(false);
    } catch (err) {
      setError(errorText(err));
    }
  };

  const point = (e: PointerEvent) => {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  return (
    <div class="scene-tab">
      <div class="toolbar">
        <strong>{scene.id}</strong>
        <span class="muted">
          {scene.duration_seconds ? `${scene.duration_seconds.toFixed(2)} 秒` : ''}
          {scene.stale && ' · 输入改过，产物已过期'}
        </span>
        <span class="spacer" />
        <button class="secondary" onClick={() => props.run('/api/commands/scene.validate', { scene_id: scene.id })}>
          校验
        </button>
        <button id="render-scene" onClick={() => props.run(`/api/scenes/${encodeURIComponent(scene.id)}/render`)}>
          {scene.exists ? '重新渲染' : '渲染'}
        </button>
      </div>
      {scene.exists ? (
        <>
          <div class="stage-frame">
            <video
              ref={video}
              controls={!selecting}
              preload="metadata"
              src={`/api/scenes/${encodeURIComponent(scene.id)}/asset?v=${props.version}`}
              onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
              onSeeked={(e) => setTime(e.currentTarget.currentTime)}
            />
            {selecting && (
              <div
                class="select-layer"
                onPointerDown={(e) => {
                  (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
                  const p = point(e);
                  setDrag({ x0: p.x, y0: p.y, x1: p.x, y1: p.y });
                }}
                onPointerMove={(e) => {
                  if (!drag || !(e.buttons & 1)) return;
                  const p = point(e);
                  setDrag({ ...drag, x1: p.x, y1: p.y });
                }}
              >
                {drag && (
                  <i
                    class="select-box"
                    style={{
                      left: Math.min(drag.x0, drag.x1),
                      top: Math.min(drag.y0, drag.y1),
                      width: Math.abs(drag.x1 - drag.x0),
                      height: Math.abs(drag.y1 - drag.y0),
                    }}
                  />
                )}
              </div>
            )}
          </div>
          <div class="toolbar">
            <button class="secondary" onClick={() => step(-1)} title="后退一帧">
              ◀ 1 帧
            </button>
            <button class="secondary" onClick={() => step(1)} title="前进一帧">
              1 帧 ▶
            </button>
            <span class="muted mono">
              {time.toFixed(2)}s · 第 {Math.round(time * FPS)} 帧
            </span>
            <span class="spacer" />
            <button class={selecting ? '' : 'secondary'} onClick={() => setSelecting(!selecting)}>
              {selecting ? '取消圈选' : '圈选区域'}
            </button>
            <button id="attach-frame" onClick={attach}>
              把这一帧{drag ? '和选区' : ''}附到对话
            </button>
          </div>
          {error && <p class="error-line">{error}</p>}
        </>
      ) : (
        <Empty>这个镜头还没有产物。点「渲染」，或在对话里让它处理。</Empty>
      )}
    </div>
  );
}

function ComposeTab(props: Props & { scene: Scene | null }) {
  const { scene } = props;
  const frame = useRef<HTMLIFrameElement>(null);
  const [picking, setPicking] = useState(false);
  const [missing, setMissing] = useState(false);

  useEffect(() => {
    setPicking(false);
    setMissing(!props.tree.some((f) => scene && f.path === relativeScene(props.state, scene) + '/index.html'));
  }, [scene?.id, props.tree]);

  // iframe 与 Studio 同源，可以直接在合成页面上挂监听，取被点中元素的选择器。
  useEffect(() => {
    const doc = frame.current?.contentDocument;
    if (!picking || !doc || !scene) return;
    let hovered: HTMLElement | null = null;
    const over = (e: Event) => {
      hovered?.style.removeProperty('outline');
      hovered = e.target as HTMLElement;
      hovered.style.outline = '2px solid #ff7a45';
    };
    const click = (e: Event) => {
      e.preventDefault();
      e.stopPropagation();
      hovered?.style.removeProperty('outline');
      props.onAttach({
        scene_id: scene.id,
        selector: cssSelector(e.target as Element),
        source: relativeScene(props.state, scene) + '/index.html',
      });
      setPicking(false);
    };
    doc.addEventListener('mouseover', over, true);
    doc.addEventListener('click', click, true);
    return () => {
      hovered?.style.removeProperty('outline');
      doc.removeEventListener('mouseover', over, true);
      doc.removeEventListener('click', click, true);
    };
  }, [picking, scene?.id]);

  if (!scene) return <Empty>在左边选一个镜头。</Empty>;
  if (missing) return <Empty>镜头目录里还没有 index.html 合成文件。</Empty>;
  return (
    <div class="compose-tab">
      <div class="toolbar">
        <strong>{scene.id}</strong>
        <span class="muted">合成实时预览：直接打开镜头目录里的 index.html</span>
        <span class="spacer" />
        <button class={picking ? '' : 'secondary'} id="pick-element" onClick={() => setPicking(!picking)}>
          {picking ? '取消选取' : '点选元素附到对话'}
        </button>
      </div>
      <iframe
        ref={frame}
        class="compose-frame"
        title="合成预览"
        src={`/preview/scenes/${encodeURIComponent(scene.id)}/?v=${props.version}`}
      />
    </div>
  );
}

function relativeScene(state: ProjectState | null, scene: Scene): string {
  const root = state?.project.path ?? '';
  return scene.directory.startsWith(root + '/') ? scene.directory.slice(root.length + 1) : scene.directory;
}

const finalCandidates = ['final.mp4', 'production/final.mp4', 'production/audio/candidate.mp4', 'production/silent-master.mp4'];

function FinalTab({ tree, version, run }: { tree: TreeFile[]; version: number; run: RunAction }) {
  const present = finalCandidates.filter((path) => tree.some((f) => f.path === path));
  const [chosen, setChosen] = useState('');
  const current = present.includes(chosen) ? chosen : present[0];
  return (
    <div class="final-tab">
      <div class="toolbar">
        {present.map((path) => (
          <button key={path} class={path === current ? '' : 'secondary'} onClick={() => setChosen(path)}>
            {path}
          </button>
        ))}
        <span class="spacer" />
        <button class="secondary" onClick={() => run('/api/commands/project.concat')}>
          拼接无声母版
        </button>
        <button class="secondary" onClick={() => run('/api/render/all')}>
          渲染全部镜头
        </button>
      </div>
      {current ? (
        <div class="stage-frame">
          <video controls preload="metadata" src={rawURL(current, version)} />
        </div>
      ) : (
        <Empty>还没有成片。镜头都出片后，可以先拼接无声母版。</Empty>
      )}
    </div>
  );
}

function StyleTab({ tree, version, onFile }: { tree: TreeFile[]; version: number; onFile: (path: string) => void }) {
  const images = tree.filter((f) => /assets\/style-guide\/.*\.png$/.test(f.path));
  const docs = tree.filter((f) => f.path === 'frame.md' || /^docs\/.*说明书\.md$/.test(f.path) || f.path === 'docs/style-brief.md');
  return (
    <div class="style-tab">
      <div class="toolbar">
        {docs.map((doc) => (
          <button key={doc.path} class="secondary" onClick={() => onFile(doc.path)}>
            {doc.path}
          </button>
        ))}
      </div>
      {images.length === 0 ? (
        <Empty>项目里还没有风格示例图。</Empty>
      ) : (
        <div class="gallery">
          {images.map((image) => (
            <figure key={image.path}>
              <img src={rawURL(image.path, version)} alt="" loading="lazy" />
              <figcaption>{image.path.split('/').pop()}</figcaption>
            </figure>
          ))}
        </div>
      )}
    </div>
  );
}

interface ReviewResponse {
  available: boolean;
  report?: { issues?: unknown[]; hints?: unknown[] } & Record<string, unknown>;
}

function ReviewTab({ tree, version, run }: { tree: TreeFile[]; version: number; run: RunAction }) {
  const [review, setReview] = useState<ReviewResponse | null>(null);
  useEffect(() => {
    api<ReviewResponse>('/api/review').then(setReview, () => setReview({ available: false }));
  }, [version]);
  const sheets = tree.filter((f) => /^production\/review\/.*\.png$/.test(f.path));
  const list = (title: string, items?: unknown[]) =>
    items && items.length > 0 ? (
      <section>
        <h4>{title}</h4>
        <ul class="issues">
          {items.map((item, index) => (
            <li key={index}>{typeof item === 'string' ? item : JSON.stringify(item)}</li>
          ))}
        </ul>
      </section>
    ) : null;
  return (
    <div class="review-tab">
      <div class="toolbar">
        <span class="muted">am review 的结果：音画时长、交界帧、联系表与响度。</span>
        <span class="spacer" />
        <button onClick={() => run('/api/review/run')}>运行复查</button>
      </div>
      {!review?.available ? (
        <Empty>还没有复查报告。</Empty>
      ) : (
        <>
          {list('问题', review.report?.issues)}
          {list('提示', review.report?.hints)}
          {!review.report?.issues?.length && !review.report?.hints?.length && <p class="ok-line">没有发现问题。</p>}
        </>
      )}
      <div class="gallery wide">
        {sheets.map((sheet) => (
          <figure key={sheet.path}>
            <img src={rawURL(sheet.path, version)} alt="" loading="lazy" />
            <figcaption>{sheet.path.split('/').pop()}</figcaption>
          </figure>
        ))}
      </div>
    </div>
  );
}

interface PublishResponse {
  available: boolean;
  content: string;
  covers: { path: string; width?: number; height?: number; error?: string }[];
}

function PublishTab({ version, run }: { version: number; run: RunAction }) {
  const [publish, setPublish] = useState<PublishResponse | null>(null);
  useEffect(() => {
    api<PublishResponse>('/api/publish').then(setPublish, () => setPublish(null));
  }, [version]);
  if (!publish) return <Empty>正在读取…</Empty>;
  return (
    <div class="publish-tab">
      <div class="toolbar">
        <span class="muted">{publish.available ? 'publish.md' : '还没有 publish.md'}</span>
        <span class="spacer" />
        <button disabled={!publish.available} onClick={() => run('/api/commands/publish.check')}>
          发布校验
        </button>
      </div>
      {publish.available && <pre class="text">{publish.content}</pre>}
      <div class="gallery">
        {publish.covers.map((cover) => (
          <figure key={cover.path}>
            <img src={rawURL(cover.path, version)} alt="" loading="lazy" />
            <figcaption>
              {cover.path.split('/').pop()} {cover.width ? `${cover.width}×${cover.height}` : cover.error}
            </figcaption>
          </figure>
        ))}
      </div>
    </div>
  );
}

const videoExt = /\.(mp4|mov|webm)$/i;
const imageExt = /\.(png|jpe?g|webp|gif|svg)$/i;
const audioExt = /\.(wav|mp3|m4a|aac|flac|ogg|opus)$/i;

interface FileResponse {
  path: string;
  size: number;
  binary: boolean;
  content: string;
}

export function FileView({ path, version }: { path: string; version: number }) {
  const [file, setFile] = useState<FileResponse | null>(null);
  const [error, setError] = useState('');
  const media = videoExt.test(path) || imageExt.test(path) || audioExt.test(path);
  useEffect(() => {
    setFile(null);
    setError('');
    if (!path || media) return;
    api<FileResponse>('/api/files?path=' + encodeURIComponent(path)).then(setFile, (err) => setError(errorText(err)));
  }, [path, version]);

  if (!path) return <Empty>在左边的文件树里选一个文件。</Empty>;
  return (
    <div class="file-tab">
      <div class="toolbar">
        <code class="path">{path}</code>
        <span class="spacer" />
        <a class="button secondary" href={rawURL(path, version)} target="_blank" rel="noreferrer">
          新窗口打开
        </a>
      </div>
      {videoExt.test(path) && (
        <div class="stage-frame">
          <video controls preload="metadata" src={rawURL(path, version)} />
        </div>
      )}
      {imageExt.test(path) && <img class="file-image" src={rawURL(path, version)} alt="" />}
      {audioExt.test(path) && <audio controls src={rawURL(path, version)} />}
      {error && <p class="error-line">{error}</p>}
      {file && (file.binary ? <Empty>二进制文件或超过 2 MB，不显示内容。</Empty> : <pre class="text" id="file-content">{file.content}</pre>)}
    </div>
  );
}
