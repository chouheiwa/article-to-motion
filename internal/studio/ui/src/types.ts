// 与 internal/studio 的 JSON 结构一一对应。改后端字段时同步改这里。

export interface Project {
  id: string;
  path: string;
  name: string;
  last_opened_at?: string;
}

export interface ProjectState {
  project: Project;
  stage: 'setup' | 'plan' | 'render' | 'assemble' | 'review' | 'publish' | string;
  metadata: Record<string, string>;
  media: { audio: boolean; final: boolean; publish: boolean };
  scenes: { total: number; completed: number; stale: number };
  issues: unknown[];
}

export interface Scene {
  id: string;
  directory: string;
  duration_seconds?: number;
  output?: string;
  exists: boolean;
  stale: boolean;
}

export interface TreeFile {
  path: string;
  size: number;
  mtime: number;
}

export interface Task {
  task_id: string;
  type: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  progress: number;
  message: string;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Attachment {
  scene_id?: string;
  path?: string;
  seconds?: number;
  region?: Box;
  selector?: string;
  source?: string;
  note?: string;
}

export interface SessionMeta {
  session_id: string;
  title: string;
  kind: 'chat' | 'production';
  tool: string;
  renderer?: string;
  cli_session_id?: string;
  created_at: string;
  updated_at: string;
  turns: string[];
  running_turn?: string;
}

export interface FileChange {
  path: string;
  change: 'added' | 'modified' | 'deleted';
  revertable: boolean;
}

export interface Usage {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_input_tokens?: number;
  cache_creation_input_tokens?: number;
}

export type EventKind =
  | 'session'
  | 'text'
  | 'thinking'
  | 'tool-call'
  | 'tool-result'
  | 'usage'
  | 'error'
  | 'done'
  | 'user'
  | 'turn-start'
  | 'turn-end'
  | 'files-changed'
  | 'notice'
  | 'turn-reverted';

export interface SessionEvent {
  seq: number;
  turn_id?: string;
  time: string;
  kind: EventKind;
  text?: string;
  tool?: string;
  call_id?: string;
  input?: unknown;
  is_error?: boolean;
  session_id?: string;
  usage?: Usage;
  status?: string;
  files?: string[];
  changes?: FileChange[];
}

export interface Turn {
  turn_id: string;
  session_id: string;
  kind: string;
  message: string;
  status: string;
  error?: string;
  started_at: string;
  ended_at?: string;
  changes: FileChange[];
}

export interface TurnFile {
  path: string;
  change: string;
  text: boolean;
  before: string;
  after: string;
}

export interface CanvasOption {
  id: string;
  label: string;
  width: number;
  height: number;
  orientation: string;
}

export interface StyleOption {
  id: string;
  name: string;
  label: string;
}

export interface StylesResponse {
  canvases: CanvasOption[];
  styles: StyleOption[];
  custom: StyleOption;
  archetypes: string[];
  default_canvas: string;
}

export interface DirectoryEntry {
  name: string;
  path: string;
  is_project: boolean;
}

export interface BeatState {
  enabled: boolean;
  draft: boolean;
  click: boolean;
  confirmed: boolean;
  click_path: string;
}

export interface AgentInfo {
  id: string;
  label: string;
  command: string;
  installed: boolean;
  path?: string;
}

export interface AgentsResponse {
  agents: AgentInfo[];
  orchestrator: string;
  renderer: string;
  config_error?: string;
}

/** 一个会话用的编排工具与渲染工具。 */
export interface AgentChoice {
  tool: string;
  renderer: string;
}
