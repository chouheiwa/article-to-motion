import { useEffect, useState } from 'preact/hooks';
import { api, errorText, post } from '../api';
import type { DirectoryEntry, Project, StylesResponse } from '../types';

interface Props {
  projects: Project[];
  currentId: string;
  onClose: () => void;
  onOpened: () => void;
}

interface Listing {
  path: string;
  parent: string;
  root: string;
  entries: DirectoryEntry[];
}

type Mode = 'recent' | 'open' | 'create';

export function ProjectDialog({ projects, currentId, onClose, onOpened }: Props) {
  const [mode, setMode] = useState<Mode>(projects.length ? 'recent' : 'open');
  const [listing, setListing] = useState<Listing | null>(null);
  const [selected, setSelected] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);

  const browse = async (path = '') => {
    try {
      const result = await api<Listing>('/api/directories' + (path ? '?path=' + encodeURIComponent(path) : ''));
      setListing(result);
      setSelected('');
      setError('');
    } catch (err) {
      setError(errorText(err));
    }
  };

  useEffect(() => {
    if (mode !== 'recent' && !listing) void browse();
  }, [mode]);

  const run = async (work: () => Promise<unknown>) => {
    setWorking(true);
    setError('');
    try {
      await work();
      onOpened();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setWorking(false);
    }
  };

  return (
    <div class="modal-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <section class="modal" role="dialog" aria-label="项目" id="project-dialog">
        <header class="modal-head">
          <div class="segmented">
            <button class={mode === 'recent' ? 'on' : ''} onClick={() => setMode('recent')} disabled={!projects.length}>
              最近项目
            </button>
            <button class={mode === 'open' ? 'on' : ''} onClick={() => setMode('open')} id="mode-open">
              打开目录
            </button>
            <button class={mode === 'create' ? 'on' : ''} onClick={() => setMode('create')} id="mode-create">
              新建项目
            </button>
          </div>
          <button class="ghost" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </header>

        {mode === 'recent' && (
          <RecentList
            projects={projects}
            currentId={currentId}
            onSwitch={(id) => run(() => post('/api/workspaces/switch', { project_id: id }))}
            onRemove={(id) => run(() => post('/api/projects/recent/remove', { project_id: id }))}
          />
        )}

        {mode !== 'recent' && (
          <DirectoryBrowser
            listing={listing}
            selected={selected}
            projectsOnly={mode === 'open'}
            onBrowse={browse}
            onSelect={setSelected}
          />
        )}

        {mode === 'open' && (
          <footer class="modal-foot">
            <code class="path">{selected || '选择一个项目目录（带 frame.md 或 scenes/）'}</code>
            <button
              id="open-project"
              disabled={!selected || working}
              onClick={() => run(() => post('/api/projects/open', { path: selected }))}
            >
              打开
            </button>
          </footer>
        )}

        {mode === 'create' && listing && (
          <CreateForm
            parent={selected || listing.path}
            working={working}
            onCreate={(payload) => run(() => post('/api/projects/create', payload))}
          />
        )}

        {error && <p class="error-line">{error}</p>}
      </section>
    </div>
  );
}

function RecentList(props: {
  projects: Project[];
  currentId: string;
  onSwitch: (id: string) => void;
  onRemove: (id: string) => void;
}) {
  const sorted = [...props.projects].sort((a, b) => (b.last_opened_at ?? '').localeCompare(a.last_opened_at ?? ''));
  return (
    <ul class="list recent">
      {sorted.map((p) => (
        <li key={p.id} class={p.id === props.currentId ? 'active' : ''}>
          <button class="row" onClick={() => props.onSwitch(p.id)}>
            <strong>{p.name}</strong>
            <span class="muted ellipsis">{p.path}</span>
          </button>
          <button class="ghost small" title="从列表移除（不删除文件）" onClick={() => props.onRemove(p.id)}>
            移除
          </button>
        </li>
      ))}
    </ul>
  );
}

function DirectoryBrowser(props: {
  listing: Listing | null;
  selected: string;
  projectsOnly: boolean;
  onBrowse: (path: string) => void;
  onSelect: (path: string) => void;
}) {
  const { listing } = props;
  if (!listing) return <p class="muted pad">正在读取目录…</p>;
  return (
    <div class="browser">
      <div class="browser-bar">
        <button class="ghost small" disabled={!listing.parent} onClick={() => props.onBrowse(listing.parent)}>
          ↑ 上一级
        </button>
        <code class="path">{listing.path}</code>
        <span class="spacer" />
        <span class="muted small">{props.projectsOnly ? '单击项目选中，单击文件夹进入' : '单击选为父目录，双击进入'}</span>
      </div>
      <ul class="list dirs">
        {listing.entries.length === 0 && <li class="muted pad">没有子目录</li>}
        {listing.entries.map((entry) => (
          <li key={entry.path} class={entry.path === props.selected ? 'active' : ''}>
            <button
              class={'row directory-row' + (entry.is_project ? ' project-row' : '')}
              onClick={() => (entry.is_project || !props.projectsOnly ? props.onSelect(entry.path) : props.onBrowse(entry.path))}
              onDblClick={() => props.onBrowse(entry.path)}
            >
              <span>{entry.is_project ? '🎬' : '📁'}</span>
              <span class="ellipsis">{entry.name}</span>
              {entry.is_project && <span class="tag">项目</span>}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function CreateForm(props: {
  parent: string;
  working: boolean;
  onCreate: (payload: { parent: string; name: string; canvas: string; style: string }) => void;
}) {
  const [styles, setStyles] = useState<StylesResponse | null>(null);
  const [name, setName] = useState('');
  const [canvas, setCanvas] = useState('');
  const [style, setStyle] = useState('');

  useEffect(() => {
    api<StylesResponse>('/api/styles').then((result) => {
      setStyles(result);
      setCanvas(result.default_canvas);
      setStyle(result.custom.id);
    });
  }, []);

  if (!styles) return <p class="muted pad">正在读取画幅与风格…</p>;
  const options = [styles.custom, ...styles.styles];
  return (
    <div class="create">
      <label class="field">
        <span>创建在</span>
        <code class="path">{props.parent}</code>
      </label>
      <label class="field">
        <span>项目名称</span>
        <input
          id="new-project-name"
          value={name}
          placeholder="例如 summer-story"
          autocomplete="off"
          onInput={(e) => setName(e.currentTarget.value)}
        />
      </label>
      <div class="field">
        <span>画幅</span>
        <div class="segmented">
          {styles.canvases.map((c) => (
            <button key={c.id} class={c.id === canvas ? 'on' : ''} onClick={() => setCanvas(c.id)}>
              {c.label}
            </button>
          ))}
        </div>
      </div>
      <div class="field">
        <span>风格</span>
        <div class="style-grid">
          {options.map((option) => (
            <button
              key={option.id}
              class={'style-card' + (option.id === style ? ' on' : '')}
              onClick={() => setStyle(option.id)}
            >
              {option.id === styles.custom.id ? (
                <div class="style-thumb custom">自主定风格</div>
              ) : (
                <img
                  class="style-thumb"
                  loading="lazy"
                  alt=""
                  src={`/api/styles/${option.id}/${canvas}/${styles.archetypes[0]}.png`}
                />
              )}
              <strong>{option.name}</strong>
              <span class="muted">{option.label.replace(/^[^：]*：/, '')}</span>
            </button>
          ))}
        </div>
      </div>
      <footer class="modal-foot">
        <span class="muted">创建后点「开始制作」，就会按项目里的 PROMPT.md 走整片流程。</span>
        <button
          id="create-project"
          disabled={!name.trim() || props.working}
          onClick={() => props.onCreate({ parent: props.parent, name: name.trim(), canvas, style })}
        >
          创建项目
        </button>
      </footer>
    </div>
  );
}
