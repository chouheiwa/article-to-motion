import { useMemo, useState } from 'preact/hooks';
import type { ProjectState, Scene, TreeFile } from '../types';

const stageLabels: Record<string, string> = {
  setup: '准备：还没有脚本',
  render: '制作：脚本已就绪，渲染镜头中',
  review: '复查：已有运行报告',
};

interface Props {
  state: ProjectState | null;
  scenes: Scene[];
  tree: TreeFile[];
  sceneId: string;
  filePath: string;
  recent: Set<string>;
  onScene: (id: string) => void;
  onFile: (path: string) => void;
}

export function Sidebar(props: Props) {
  const { state } = props;
  return (
    <aside class="pane left">
      {state && (
        <section class="block stage" id="stage">
          <h3>阶段</h3>
          <p>{stageLabels[state.stage] ?? state.stage}</p>
          <div class="meter" title={`${state.scenes.completed}/${state.scenes.total}`}>
            <i style={{ width: `${state.scenes.total ? (state.scenes.completed / state.scenes.total) * 100 : 0}%` }} />
          </div>
          <p class="muted">
            镜头 {state.scenes.completed}/{state.scenes.total} 已出片
            {state.scenes.stale > 0 && <span class="warn"> · {state.scenes.stale} 个过期</span>}
          </p>
          {state.metadata.style_name && <p class="muted">风格：{state.metadata.style_name}</p>}
        </section>
      )}

      <section class="block">
        <h3>镜头</h3>
        {props.scenes.length === 0 && <p class="muted">还没有镜头。开始制作后会在这里出现。</p>}
        <ul class="list scenes">
          {props.scenes.map((scene) => (
            <li key={scene.id} class={scene.id === props.sceneId ? 'active' : ''}>
              <button class="row scene" onClick={() => props.onScene(scene.id)}>
                <span class={'dot ' + (scene.stale ? 'stale' : scene.exists ? 'ok' : 'todo')} />
                <span class="ellipsis">{scene.id}</span>
                <span class="muted small">
                  {scene.duration_seconds ? scene.duration_seconds.toFixed(1) + 's' : ''}
                  {scene.stale ? ' 过期' : scene.exists ? '' : ' 未出片'}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </section>

      <section class="block grow">
        <h3>文件</h3>
        <FileTree files={props.tree} selected={props.filePath} recent={props.recent} onFile={props.onFile} />
      </section>
    </aside>
  );
}

interface Dir {
  name: string;
  path: string;
  dirs: Map<string, Dir>;
  files: TreeFile[];
}

function buildTree(files: TreeFile[]): Dir {
  const root: Dir = { name: '', path: '', dirs: new Map(), files: [] };
  for (const file of files) {
    const parts = file.path.split('/');
    let dir = root;
    for (const part of parts.slice(0, -1)) {
      let next = dir.dirs.get(part);
      if (!next) {
        next = { name: part, path: dir.path ? `${dir.path}/${part}` : part, dirs: new Map(), files: [] };
        dir.dirs.set(part, next);
      }
      dir = next;
    }
    dir.files.push(file);
  }
  return root;
}

function FileTree(props: { files: TreeFile[]; selected: string; recent: Set<string>; onFile: (path: string) => void }) {
  const root = useMemo(() => buildTree(props.files), [props.files]);
  const [open, setOpen] = useState<Set<string>>(() => new Set(['scenes', 'production']));
  const toggle = (path: string) =>
    setOpen((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const render = (dir: Dir, depth: number) => (
    <>
      {[...dir.dirs.values()]
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((child) => (
          <li key={child.path}>
            <button class="row tree-dir" style={{ paddingLeft: 8 + depth * 12 }} onClick={() => toggle(child.path)}>
              <span class="caret">{open.has(child.path) ? '▾' : '▸'}</span>
              <span class="ellipsis">{child.name}</span>
            </button>
            {open.has(child.path) && <ul class="list">{render(child, depth + 1)}</ul>}
          </li>
        ))}
      {dir.files.map((file) => (
        <li key={file.path} class={file.path === props.selected ? 'active' : ''}>
          <button
            class={'row tree-file' + (props.recent.has(file.path) ? ' recent' : '')}
            style={{ paddingLeft: 20 + depth * 12 }}
            title={file.path}
            onClick={() => props.onFile(file.path)}
          >
            <span class="ellipsis">{file.path.split('/').pop()}</span>
          </button>
        </li>
      ))}
    </>
  );

  return <ul class="list tree">{render(root, 0)}</ul>;
}
