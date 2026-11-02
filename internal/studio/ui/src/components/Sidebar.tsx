import { useMemo, useState } from 'preact/hooks';
import type { ProjectState, TreeFile } from '../types';

// 与 internal/studio/studio.go 的 stageFor 一一对应，顺序就是制作顺序。
const stages: [string, string][] = [
  ['setup', '准备'],
  ['plan', '脚本与分镜'],
  ['render', '渲染'],
  ['assemble', '合成'],
  ['review', '复查'],
  ['publish', '发布'],
];

interface Props {
  state: ProjectState | null;
  tree: TreeFile[];
  filePath: string;
  recent: Set<string>;
  onFile: (path: string) => void;
}

export function Sidebar(props: Props) {
  const { state } = props;
  const current = state ? stages.findIndex(([id]) => id === state.stage) : -1;
  const changed = props.tree.filter((f) => props.recent.has(f.path)).length;
  return (
    <aside class="pane left">
      {state && (
        <section id="stage" aria-label="制作阶段">
          <div class="section-head">
            <span class="label">制作阶段</span>
          </div>
          <ol class="stages">
            {stages.map(([id, name], index) => {
              const status = index < current ? 'done' : index === current ? 'now' : '';
              return (
                <li key={id} class={status} aria-current={status === 'now' ? 'step' : undefined}>
                  <div class="stage-rail" aria-hidden="true">
                    <span class="stage-dot" />
                    {index < stages.length - 1 && <span class="stage-line" />}
                  </div>
                  <div class="stage-body">
                    <div class="stage-name">{name}</div>
                    {status === 'now' && state.scenes.total > 0 && (
                      <div class="stage-detail">
                        <div class="meter" title={`${state.scenes.completed}/${state.scenes.total}`}>
                          <i style={{ width: `${(state.scenes.completed / state.scenes.total) * 100}%` }} />
                        </div>
                        <span>
                          {state.scenes.completed} / {state.scenes.total} 镜头已出片
                          {state.scenes.stale > 0 && <span class="warn"> · {state.scenes.stale} 个过期</span>}
                        </span>
                      </div>
                    )}
                  </div>
                </li>
              );
            })}
          </ol>
          <div class="rule" />
        </section>
      )}

      <section aria-label="文件" style={{ flex: 1, minHeight: 0 }}>
        <div class="section-head">
          <span class="label">文件</span>
          {changed > 0 && <span class="recent-count">● 本轮改动 {changed}</span>}
        </div>
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
            <button
              class="row tree-dir"
              style={{ paddingLeft: 8 + depth * 14 }}
              aria-expanded={open.has(child.path)}
              onClick={() => toggle(child.path)}
            >
              <span class="caret" aria-hidden="true">
                {open.has(child.path) ? '▾' : '▸'}
              </span>
              <span class="ellipsis">{child.name}</span>
            </button>
            {open.has(child.path) && <ul class="list">{render(child, depth + 1)}</ul>}
          </li>
        ))}
      {dir.files.map((file) => (
        <li key={file.path} class={file.path === props.selected ? 'active' : ''}>
          <button
            class={'row tree-file' + (props.recent.has(file.path) ? ' recent' : '')}
            style={{ paddingLeft: 26 + depth * 14 }}
            title={file.path}
            onClick={() => props.onFile(file.path)}
          >
            <span class="ellipsis">{file.path.split('/').pop()}</span>
            {props.recent.has(file.path) && <span class="mark" aria-label="本轮改动" />}
          </button>
        </li>
      ))}
    </>
  );

  return <ul class="list tree">{render(root, 0)}</ul>;
}
