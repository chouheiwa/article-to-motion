import { useCallback, useEffect, useRef, useState } from 'preact/hooks';
import { api, errorText, post } from './api';
import { Chat } from './components/Chat';
import { Icon } from './components/Icon';
import { Preview, type Tab } from './components/Preview';
import { ProjectDialog } from './components/ProjectDialog';
import { Sidebar } from './components/Sidebar';
import { Tasks } from './components/Tasks';
import type {
  AgentChoice,
  AgentsResponse,
  Attachment,
  BeatState,
  Project,
  ProjectState,
  Scene,
  SessionMeta,
  Task,
  TreeFile,
} from './types';

const SESSION_KEY = 'am-studio-session:';

function remember(projectId: string, sessionId: string) {
  try {
    if (sessionId) localStorage.setItem(SESSION_KEY + projectId, sessionId);
    else localStorage.removeItem(SESSION_KEY + projectId);
  } catch {
    // 隐私模式下 localStorage 不可用，只是少了「记住上次的会话」。
  }
}

function recall(projectId: string): string {
  try {
    return localStorage.getItem(SESSION_KEY + projectId) ?? '';
  } catch {
    return '';
  }
}

export function App() {
  const [connected, setConnected] = useState(true);
  const [projects, setProjects] = useState<Project[]>([]);
  const [currentId, setCurrentId] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [state, setState] = useState<ProjectState | null>(null);
  const [scenes, setScenes] = useState<Scene[]>([]);
  const [tree, setTree] = useState<TreeFile[]>([]);
  const [beat, setBeat] = useState<BeatState | null>(null);
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [busy, setBusy] = useState('');
  const [sessionId, setSessionId] = useState('');
  const [tasks, setTasks] = useState<Task[]>([]);
  const [tab, setTab] = useState<Tab>('scene');
  const [sceneId, setSceneId] = useState('');
  const [filePath, setFilePath] = useState('');
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [recent, setRecent] = useState<Set<string>>(new Set());
  const [version, setVersion] = useState(1);
  const [dialog, setDialog] = useState(false);
  const [toast, setToast] = useState('');
  const [agents, setAgents] = useState<AgentsResponse | null>(null);
  // 新对话还没建会话时选的工具；建了会话以后以会话记录为准。
  const [pending, setPending] = useState<AgentChoice>({ tool: '', renderer: '' });

  const notify = useCallback((message: string) => {
    setToast(message);
    window.setTimeout(() => setToast((current) => (current === message ? '' : current)), 5000);
  }, []);

  const loadProjects = useCallback(async () => {
    try {
      const result = await api<{ projects: Project[]; current_project_id: string }>('/api/projects');
      setProjects(result.projects ?? []);
      setCurrentId(result.current_project_id);
      setConnected(true);
      return result.current_project_id;
    } catch {
      setConnected(false);
      return '';
    } finally {
      setLoaded(true);
    }
  }, []);

  const loadSessions = useCallback(async () => {
    try {
      const result = await api<{ sessions: SessionMeta[]; busy: string }>('/api/sessions');
      setSessions(result.sessions ?? []);
      setBusy(result.busy);
    } catch {
      setSessions([]);
    }
  }, []);

  const loadProject = useCallback(async () => {
    try {
      const [st, sc, tr, bt] = await Promise.all([
        api<ProjectState>('/api/state'),
        api<{ scenes: Scene[] }>('/api/scenes'),
        api<{ files: TreeFile[] }>('/api/tree'),
        api<BeatState>('/api/beat'),
      ]);
      setState(st);
      setScenes(sc.scenes ?? []);
      setTree(tr.files ?? []);
      setBeat(bt);
      setVersion((v) => v + 1);
    } catch (err) {
      notify(errorText(err));
    }
    await loadSessions();
  }, [loadSessions, notify]);

  const loadTasks = useCallback(async () => {
    try {
      const result = await api<{ tasks: Task[] }>('/api/tasks');
      setTasks((result.tasks ?? []).sort((a, b) => b.created_at.localeCompare(a.created_at)));
    } catch {
      // 任务列表拿不到不影响其他功能。
    }
  }, []);

  useEffect(() => {
    void loadProjects();
  }, []);

  // 切换项目：清掉上一个项目的选择，恢复这个项目上次打开的会话。
  useEffect(() => {
    setState(null);
    setScenes([]);
    setTree([]);
    setSceneId('');
    setFilePath('');
    setAttachments([]);
    setRecent(new Set());
    setSessionId(currentId ? recall(currentId) : '');
    setAgents(null);
    if (currentId) {
      void loadProject();
      void loadTasks();
      api<AgentsResponse>('/api/agents').then(
        (result) => {
          setAgents(result);
          setPending({ tool: result.orchestrator, renderer: result.renderer });
        },
        () => setAgents(null),
      );
    }
  }, [currentId]);

  useEffect(() => {
    if (!sceneId && scenes.length) setSceneId(scenes[0].id);
  }, [scenes]);

  // 任务在跑时每两秒查一次，结束时刷新项目；空闲时十秒一次，顺带探测连接。
  const activeTasks = tasks.some((t) => t.status === 'queued' || t.status === 'running');
  const wasActive = useRef(false);
  useEffect(() => {
    if (wasActive.current && !activeTasks) void loadProject();
    wasActive.current = activeTasks;
    if (!currentId) return;
    const timer = window.setInterval(
      () => {
        void loadTasks();
        if (!activeTasks) void loadSessions();
      },
      activeTasks ? 2000 : 10000,
    );
    return () => window.clearInterval(timer);
  }, [activeTasks, currentId]);

  // 一轮进行中文件会陆续变化；合并成每秒最多刷新一次。
  const pendingRefresh = useRef(0);
  const onFilesChanged = useCallback(
    (files: string[]) => {
      setRecent((current) => new Set([...current, ...files]));
      if (pendingRefresh.current) return;
      pendingRefresh.current = window.setTimeout(() => {
        pendingRefresh.current = 0;
        void loadProject();
      }, 1000);
    },
    [loadProject],
  );

  const session = sessions.find((s) => s.session_id === sessionId);
  const choice: AgentChoice = session
    ? { tool: session.tool, renderer: session.renderer || agents?.renderer || '' }
    : pending;

  const changeChoice = async (next: AgentChoice) => {
    if (!session) {
      setPending(next);
      return;
    }
    try {
      await post(`/api/sessions/${session.session_id}/agents`, next);
      await loadSessions();
    } catch (err) {
      notify(errorText(err));
    }
  };

  // 渲染按钮用当前会话（或新对话）选的渲染工具。
  const renderer = choice.renderer;
  const run = useCallback(
    async (path: string, payload: Record<string, unknown> = {}) => {
      try {
        await post(path, /\/render(\/all)?$/.test(path) ? { ...payload, renderer } : payload);
        notify('已开始，进度见右上角任务。');
        await loadTasks();
        await loadSessions();
      } catch (err) {
        notify(errorText(err));
      }
    },
    [loadTasks, loadSessions, notify, renderer],
  );

  const chooseSession = (id: string) => {
    setSessionId(id);
    remember(currentId, id);
  };

  const project = projects.find((p) => p.id === currentId);

  return (
    <div class="app">
      <header class="topbar">
        <div class="brand" title="ArticleToMotion Studio">
          <span class="brand-mark" aria-hidden="true">
            AM
          </span>
          <span class="brand-name">Studio</span>
        </div>
        <span class="divider" aria-hidden="true" />
        <button class="project-switch" id="open-project-trigger" onClick={() => setDialog(true)}>
          <strong id="title" class="ellipsis">
            {project?.name ?? '选择项目'}
          </strong>
          {project && <span class="path ellipsis">{project.path}</span>}
          <Icon name="chevron" />
        </button>
        {state?.metadata.canvas && (
          <span class="chip mono">
            {state.metadata.canvas}
            {state.metadata.fps ? ` · ${state.metadata.fps}fps` : ''}
          </span>
        )}
        {state?.metadata.style_name && <span class="chip">{state.metadata.style_name}</span>}
        <span class="spacer" />
        {busy && (
          <span class="pill busy" title="同一时间只有一个写入者">
            <span class="dot busy" />
            {busy}
          </span>
        )}
        <Tasks tasks={tasks} onCancel={(id) => run(`/api/tasks/${id}/cancel`)} />
        <span class="pill conn" title={connected ? '已连接' : '连接断开'}>
          <span class={'dot ' + (connected ? 'ok' : 'bad')} />
          <span>{connected ? '已连接' : '连接断开'}</span>
        </span>
      </header>

      {loaded && !currentId ? (
        <div class="welcome">
          <h1>先打开或新建一个项目</h1>
          <p class="muted">Studio 在项目目录里启动编排工具，你在对话里说要改什么，它直接改项目文件。</p>
          <button class="primary" onClick={() => setDialog(true)}>
            选择项目
          </button>
        </div>
      ) : (
        <div class="workspace">
          <Sidebar
            state={state}
            tree={tree}
            filePath={filePath}
            recent={recent}
            onFile={(path) => {
              setFilePath(path);
              setTab('file');
            }}
          />
          <Preview
            tab={tab}
            onTab={setTab}
            state={state}
            scenes={scenes}
            sceneId={sceneId}
            filePath={filePath}
            tree={tree}
            version={version}
            beat={beat}
            busy={busy}
            onAttach={(attachment) => {
              setAttachments((current) => [...current, attachment]);
              notify('已附到对话，发送消息时一起带上。');
            }}
            onFile={(path) => {
              setFilePath(path);
              setTab('file');
            }}
            onScene={(id) => {
              setSceneId(id);
              if (tab !== 'scene' && tab !== 'compose') setTab('scene');
            }}
            run={run}
          />
          <Chat
            agents={agents}
            choice={choice}
            onChoice={changeChoice}
            sessions={sessions}
            sessionId={sessionId}
            busy={busy}
            attachments={attachments}
            onSession={chooseSession}
            onSessionsChanged={loadSessions}
            onRemoveAttachment={(index) => setAttachments((current) => current.filter((_, i) => i !== index))}
            onClearAttachments={() => setAttachments([])}
            onFilesChanged={onFilesChanged}
            onTurnEnd={() => void loadProject()}
            onOpenFile={(path) => {
              setFilePath(path);
              setTab('file');
            }}
          />
        </div>
      )}

      {dialog && (
        <ProjectDialog
          projects={projects}
          currentId={currentId}
          onClose={() => setDialog(false)}
          onOpened={async () => {
            setDialog(false);
            await loadProjects();
          }}
        />
      )}
      {toast && (
        <div class="toast" role="status">
          {toast}
        </div>
      )}
    </div>
  );
}
