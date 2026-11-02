import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { ApiError, errorText, post, rawURL } from '../api';
import type { AgentChoice, AgentsResponse, Attachment, EventKind, FileChange, SessionEvent, SessionMeta, Usage } from '../types';
import { AgentPicker } from './AgentPicker';
import { DiffDialog } from './DiffDialog';
import { Icon } from './Icon';

// SSE 的 event 字段就是事件种类，EventSource 只把具名事件派给对应监听器，所以要逐个注册。
const kinds: EventKind[] = [
  'session',
  'text',
  'thinking',
  'tool-call',
  'tool-result',
  'usage',
  'error',
  'done',
  'user',
  'turn-start',
  'turn-end',
  'files-changed',
  'notice',
  'turn-reverted',
];

interface Props {
  agents: AgentsResponse | null;
  choice: AgentChoice;
  onChoice: (choice: AgentChoice) => void;
  sessions: SessionMeta[];
  sessionId: string;
  busy: string;
  attachments: Attachment[];
  onSession: (id: string) => void;
  onSessionsChanged: () => void;
  onRemoveAttachment: (index: number) => void;
  onClearAttachments: () => void;
  onFilesChanged: (files: string[]) => void;
  onTurnEnd: () => void;
  onOpenFile: (path: string) => void;
}

export function Chat(props: Props) {
  const events = useSessionEvents(props.sessionId, props.onFilesChanged, props.onTurnEnd);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);

  const running = useMemo(() => {
    let open = false;
    for (const event of events) {
      if (event.kind === 'turn-start') open = true;
      if (event.kind === 'turn-end') open = false;
    }
    return open;
  }, [events]);

  useEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [events]);

  const blocked = running || sending || (props.busy !== '' && !running);

  const send = async () => {
    const message = draft.trim();
    if (!message || blocked) return;
    setSending(true);
    setError('');
    try {
      let id = props.sessionId;
      if (!id) {
        const created = await post<SessionMeta>('/api/sessions', { title: message.slice(0, 24), ...props.choice });
        id = created.session_id;
        props.onSession(id);
      }
      await post(`/api/sessions/${id}/turns`, { message, attachments: props.attachments });
      setDraft('');
      props.onClearAttachments();
      props.onSessionsChanged();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setSending(false);
    }
  };

  const startProduction = async () => {
    setSending(true);
    setError('');
    try {
      const result = await post<{ session_id: string }>('/api/production/start', { message: draft.trim(), ...props.choice });
      setDraft('');
      props.onSession(result.session_id);
      props.onSessionsChanged();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setSending(false);
    }
  };

  const cancel = async () => {
    try {
      await post(`/api/sessions/${props.sessionId}/cancel`);
    } catch (err) {
      setError(errorText(err));
    }
  };

  return (
    <aside class="pane right chat">
      <header class="chat-head">
        <select
          id="session-select"
          value={props.sessionId}
          onChange={(e) => props.onSession(e.currentTarget.value)}
          aria-label="会话"
        >
          <option value="">新对话</option>
          {props.sessions.map((s) => (
            <option key={s.session_id} value={s.session_id}>
              {s.title}
              {s.kind === 'production' ? ' · 制作' : ''}
              {s.turns.length ? ` · ${s.turns.length} 轮` : ''}
            </option>
          ))}
        </select>
        <button class="quiet" onClick={() => props.onSession('')} title="新开一个对话">
          新对话
        </button>
        <button id="start-production" class="primary" disabled={blocked} onClick={startProduction} title="与 am run 相同的整片制作">
          开始制作
        </button>
      </header>

      <div
        class="messages"
        id="messages"
        ref={scroller}
        onScroll={(e) => {
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
        }}
      >
        {events.length === 0 && (
          <div class="intro">
            <p>在这里说要改什么，编排工具会直接改项目文件；每一轮的改动都能查看和回退。</p>
            <p class="muted">新项目先点「开始制作」，它会照 PROMPT.md 从头做到成片，之后在同一个会话里继续改。</p>
          </div>
        )}
        <Transcript events={events} onOpenFile={props.onOpenFile} onReverted={props.onTurnEnd} />
        {running && (
          <div class="typing">
            <span class="dot busy" />
            正在处理…
          </div>
        )}
      </div>

      <footer class="composer">
        {props.busy && !running && <p class="busy-line">项目正忙：{props.busy}</p>}
        <div class="composer-box">
          {props.attachments.length > 0 && (
            <ul class="chips" id="attachments">
              {props.attachments.map((a, index) => (
                <li key={index}>
                  {a.path && <img src={rawURL(a.path)} alt="" />}
                  <span>
                    {a.scene_id}
                    {a.seconds !== undefined && a.path ? ` · 帧 ${Math.round(a.seconds * 30)}` : ''}
                    {a.region ? ' · 区域' : ''}
                    {a.selector ? ` · ${a.selector}` : ''}
                  </span>
                  <button class="ghost" onClick={() => props.onRemoveAttachment(index)} aria-label="移除附件">
                    <Icon name="close" size={10} />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <label for="prompt" class="sr-only">
            消息
          </label>
          <textarea
            id="prompt"
            rows={2}
            value={draft}
            placeholder={running ? '这一轮还在进行…' : '说说要改什么……（⌘/Ctrl + Enter 发送）'}
            onInput={(e) => setDraft(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                void send();
              }
            }}
          />
          <div class="composer-actions">
            <AgentPicker agents={props.agents} value={props.choice} disabled={running || sending} onChange={props.onChoice} />
            <span class="spacer" />
            {running ? (
              <button id="cancel-turn" class="primary" onClick={cancel} aria-label="停止本轮" title="停止本轮">
                <Icon name="stop" />
              </button>
            ) : (
              <button id="send" class="primary" disabled={blocked || !draft.trim()} onClick={send} aria-label="发送" title="发送">
                <Icon name="send" size={14} />
              </button>
            )}
          </div>
        </div>
        {error && <p class="error-line">{error}</p>}
      </footer>
    </aside>
  );
}

/** 订阅一个会话的事件流。EventSource 断线后自带 Last-Event-ID 重连，后端按 seq 补发，这里再按 seq 去重。 */
function useSessionEvents(sessionId: string, onFilesChanged: (files: string[]) => void, onTurnEnd: () => void) {
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const callbacks = useRef({ onFilesChanged, onTurnEnd });
  callbacks.current = { onFilesChanged, onTurnEnd };

  useEffect(() => {
    setEvents([]);
    if (!sessionId) return;
    let last = 0;
    let pending: SessionEvent[] = [];
    let frame = 0;
    // 重放历史时一次会来很多条，攒到下一帧一起更新，避免逐条重渲染。
    const flush = () => {
      frame = 0;
      const batch = pending;
      pending = [];
      setEvents((current) => current.concat(batch));
    };
    const source = new EventSource(`/api/sessions/${sessionId}/events`);
    const handle = (message: MessageEvent<string>) => {
      const event = JSON.parse(message.data) as SessionEvent;
      if (event.seq <= last) return;
      last = event.seq;
      pending.push(event);
      if (!frame) frame = requestAnimationFrame(flush);
      if (event.kind === 'files-changed' && event.files) callbacks.current.onFilesChanged(event.files);
      if (event.kind === 'turn-end') callbacks.current.onTurnEnd();
    };
    for (const kind of kinds) source.addEventListener(kind, handle as EventListener);
    return () => {
      source.close();
      if (frame) cancelAnimationFrame(frame);
    };
  }, [sessionId]);

  return events;
}

type Item =
  | { type: 'event'; event: SessionEvent }
  | { type: 'tool'; call: SessionEvent; result?: SessionEvent }
  | { type: 'turn-end'; event: SessionEvent; usage?: Usage; reverted: boolean };

function group(events: SessionEvent[]): Item[] {
  const items: Item[] = [];
  const calls = new Map<string, { type: 'tool'; call: SessionEvent; result?: SessionEvent }>();
  const usage = new Map<string, Usage>();
  const reverted = new Set(events.filter((e) => e.kind === 'turn-reverted').map((e) => e.turn_id));
  for (const event of events) {
    switch (event.kind) {
      case 'session':
      case 'done':
      case 'turn-start':
      case 'turn-reverted':
        break;
      case 'usage':
        if (event.turn_id && event.usage) usage.set(event.turn_id, event.usage);
        break;
      case 'tool-call': {
        const item = { type: 'tool' as const, call: event };
        if (event.call_id) calls.set(event.call_id, item);
        items.push(item);
        break;
      }
      case 'tool-result': {
        const owner = event.call_id ? calls.get(event.call_id) : undefined;
        if (owner) owner.result = event;
        else items.push({ type: 'event', event });
        break;
      }
      case 'turn-end':
        items.push({ type: 'turn-end', event, usage: usage.get(event.turn_id ?? ''), reverted: reverted.has(event.turn_id) });
        break;
      default:
        items.push({ type: 'event', event });
    }
  }
  return items;
}

function Transcript(props: { events: SessionEvent[]; onOpenFile: (path: string) => void; onReverted: () => void }) {
  const items = useMemo(() => group(props.events), [props.events]);
  return (
    <>
      {items.map((item) => {
        if (item.type === 'tool') return <ToolCard key={item.call.seq} call={item.call} result={item.result} />;
        if (item.type === 'turn-end')
          return (
            <TurnCard
              key={item.event.seq}
              event={item.event}
              usage={item.usage}
              reverted={item.reverted}
              onOpenFile={props.onOpenFile}
              onReverted={props.onReverted}
            />
          );
        const { event } = item;
        switch (event.kind) {
          case 'user':
            return (
              <div key={event.seq} class="message user">
                {event.text}
              </div>
            );
          case 'text':
            return (
              <div key={event.seq} class="message assistant">
                {event.text}
              </div>
            );
          case 'thinking':
            return (
              <details key={event.seq} class="thinking">
                <summary>思考</summary>
                <p>{event.text}</p>
              </details>
            );
          case 'files-changed':
            return (
              <div key={event.seq} class="files-line" title={event.files?.join('\n')}>
                改动了 {event.files?.length ?? 0} 个文件
              </div>
            );
          case 'error':
            return (
              <div key={event.seq} class="notice error">
                {event.text}
              </div>
            );
          default:
            return (
              <div key={event.seq} class="notice">
                {event.text}
              </div>
            );
        }
      })}
    </>
  );
}

function summarizeInput(input: unknown): string {
  if (!input || typeof input !== 'object') return '';
  const record = input as Record<string, unknown>;
  for (const key of ['command', 'file_path', 'path', 'pattern', 'url', 'description']) {
    if (typeof record[key] === 'string') return record[key] as string;
  }
  const text = JSON.stringify(input);
  return text.length > 120 ? text.slice(0, 120) + '…' : text;
}

function ToolCard({ call, result }: { call: SessionEvent; result?: SessionEvent }) {
  const state = !result ? 'running' : result.is_error ? 'failed' : 'done';
  return (
    <details class={'tool ' + state}>
      <summary>
        <span class={'dot ' + (state === 'running' ? 'busy' : state === 'failed' ? 'bad' : 'ok')} />
        <span class="tool-name">{call.tool}</span>
        <span class="tool-input ellipsis">{summarizeInput(call.input)}</span>
        <span class="tool-state">{state === 'running' ? '运行中' : state === 'failed' ? '失败' : '完成'}</span>
      </summary>
      {call.input !== undefined && <pre>{JSON.stringify(call.input, null, 2)}</pre>}
      {result?.text && <pre class="tool-output">{result.text}</pre>}
    </details>
  );
}

const statusLabels: Record<string, string> = {
  succeeded: '本轮完成',
  failed: '本轮失败',
  cancelled: '本轮已停止',
  interrupted: '本轮被中断',
};

const changeLabels: Record<FileChange['change'], string> = { added: '新增', modified: '修改', deleted: '删除' };

function TurnCard(props: {
  event: SessionEvent;
  usage?: Usage;
  reverted: boolean;
  onOpenFile: (path: string) => void;
  onReverted: () => void;
}) {
  const { event } = props;
  const changes = event.changes ?? [];
  const [diffPath, setDiffPath] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const revert = async () => {
    setWorking(true);
    setError('');
    try {
      await post(`/api/turns/${event.turn_id}/revert`);
      props.onReverted();
    } catch (err) {
      const paths = err instanceof ApiError && Array.isArray(err.body.paths) ? `：${(err.body.paths as string[]).join('、')}` : '';
      setError(errorText(err) + paths);
    } finally {
      setWorking(false);
    }
  };
  const tokens = props.usage ? (props.usage.input_tokens ?? 0) + (props.usage.output_tokens ?? 0) : 0;
  return (
    <section class={'turn-card ' + (event.status ?? '')} id={'turn-' + event.turn_id}>
      <header>
        <strong>{props.reverted ? '已回退这一轮' : statusLabels[event.status ?? ''] ?? event.status}</strong>
        {changes.length > 0 && <span class="small muted">本轮改动 {changes.length} 个文件</span>}
        {tokens > 0 && <span class="muted small mono">{tokens.toLocaleString()} tokens</span>}
      </header>
      {event.text && <p class="error-line">{event.text}</p>}
      {changes.length === 0 ? (
        <p class="muted">这一轮没有改动项目文件。</p>
      ) : (
        <ul class="changes">
          {changes.map((change) => (
            <li key={change.path}>
              <span class={'change ' + change.change}>{changeLabels[change.change]}</span>
              <button
                class="link ellipsis"
                onClick={() => (change.revertable ? setDiffPath(change.path) : props.onOpenFile(change.path))}
                title={change.revertable ? '查看改动' : '媒体或大文件：只列出，不回退'}
              >
                {change.path}
              </button>
              {!change.revertable && <span class="muted small">不回退</span>}
            </li>
          ))}
        </ul>
      )}
      {changes.length > 0 && !props.reverted && (
        <footer>
          <button class="quiet revert" id="revert-turn" disabled={working} onClick={revert}>
            回退本轮
          </button>
        </footer>
      )}
      {error && <p class="error-line">{error}</p>}
      {diffPath && <DiffDialog turnId={event.turn_id ?? ''} path={diffPath} onClose={() => setDiffPath('')} />}
    </section>
  );
}
