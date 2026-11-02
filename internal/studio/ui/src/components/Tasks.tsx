import { useState } from 'preact/hooks';
import type { Task } from '../types';

const statusLabels: Record<Task['status'], string> = {
  queued: '排队',
  running: '进行中',
  succeeded: '完成',
  failed: '失败',
  cancelled: '已取消',
};

export function Tasks({ tasks, onCancel }: { tasks: Task[]; onCancel: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const active = tasks.filter((t) => t.status === 'queued' || t.status === 'running').length;
  return (
    <div class="tasks">
      <button class="quiet" id="tasks-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
        任务
        {tasks.length > 0 && <span class={'count' + (active ? ' on' : '')}>{active || tasks.length}</span>}
      </button>
      {open && (
        <div class="popover" id="tasks">
          {tasks.length === 0 && <p class="muted pad">还没有任务。</p>}
          {tasks.slice(0, 20).map((task) => (
            <div key={task.task_id} class={'task ' + task.status}>
              <div class="task-head">
                <strong>{task.message.split('\n')[0]}</strong>
                <span class="muted small">{statusLabels[task.status]}</span>
              </div>
              {task.status === 'running' && (
                <div class="meter">
                  <i style={{ width: `${Math.round(task.progress * 100)}%` }} />
                </div>
              )}
              {task.error && <pre class="task-error">{task.error}</pre>}
              {(task.status === 'running' || task.status === 'queued') && (
                <button class="ghost" onClick={() => onCancel(task.task_id)}>
                  取消
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
