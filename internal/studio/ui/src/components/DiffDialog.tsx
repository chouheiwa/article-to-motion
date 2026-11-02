import { useEffect, useMemo, useState } from 'preact/hooks';
import { api, errorText } from '../api';
import { hunks, lineDiff } from '../diff';
import type { TurnFile } from '../types';

export function DiffDialog({ turnId, path, onClose }: { turnId: string; path: string; onClose: () => void }) {
  const [file, setFile] = useState<TurnFile | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    api<TurnFile>(`/api/turns/${turnId}/file?path=${encodeURIComponent(path)}`).then(setFile, (err) => setError(errorText(err)));
  }, [turnId, path]);
  const lines = useMemo(() => (file?.text ? hunks(lineDiff(file.before, file.after)) : []), [file]);
  return (
    <div class="modal-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <section class="modal wide" role="dialog" aria-label="改动">
        <header class="modal-head">
          <code class="path">{path}</code>
          <button class="ghost" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </header>
        {error && <p class="error-line">{error}</p>}
        {file && !file.text && <p class="muted pad">二进制文件，不显示差异。</p>}
        {file?.text && (
          <pre class="diff" id="diff">
            {lines.map((line, index) =>
              line.kind === '…' ? (
                <div key={index} class="skip">
                  ⋯ {line.count} 行未改动
                </div>
              ) : (
                <div key={index} class={line.kind === '+' ? 'add' : line.kind === '-' ? 'del' : ''}>
                  {line.kind} {line.text}
                </div>
              ),
            )}
          </pre>
        )}
      </section>
    </div>
  );
}
