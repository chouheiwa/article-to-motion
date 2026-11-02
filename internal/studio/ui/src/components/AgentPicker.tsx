import type { AgentChoice, AgentsResponse } from '../types';

interface Props {
  agents: AgentsResponse | null;
  value: AgentChoice;
  disabled: boolean;
  onChange: (choice: AgentChoice) => void;
}

/** 编排工具与渲染工具的选择，放在输入框底部。没装的工具可见但不可选，免得选了才在运行时报找不到命令。 */
export function AgentPicker({ agents, value, disabled, onChange }: Props) {
  if (!agents) return null;
  const options = (current: string, fallback: string) =>
    agents.agents.map((agent) => (
      <option key={agent.id} value={agent.id} disabled={!agent.installed && agent.id !== current} title={agent.path ?? `找不到 ${agent.command}`}>
        {agent.label}
        {agent.installed ? '' : '（未安装）'}
        {agent.id === fallback ? ' · 默认' : ''}
      </option>
    ));
  return (
    <>
      <label class="agent-select" title="在项目目录里对话、改文件的工具">
        <span>编排</span>
        <select id="agent-tool" value={value.tool} disabled={disabled} onChange={(e) => onChange({ ...value, tool: e.currentTarget.value })}>
          {options(value.tool, agents.orchestrator)}
        </select>
      </label>
      <label class="agent-select" title="am scene run 渲染单个镜头用的工具">
        <span>渲染</span>
        <select
          id="agent-renderer"
          value={value.renderer}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, renderer: e.currentTarget.value })}
        >
          {options(value.renderer, agents.renderer)}
        </select>
      </label>
      {agents.config_error && <span class="error-line agent-error">{agents.config_error}</span>}
    </>
  );
}
