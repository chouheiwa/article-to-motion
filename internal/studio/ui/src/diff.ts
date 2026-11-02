// 逐行差异，用于「本轮改动」里查看一个文件改了什么。先去掉首尾相同的行，
// 中间部分做 LCS；中间部分过大时不做逐行比对，直接整段标为删除加新增。

export type DiffLine = { kind: ' ' | '-' | '+'; text: string };

const maxCells = 4_000_000;

export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.split('\n');
  const b = after.split('\n');
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const head = a.slice(0, start).map((text) => ({ kind: ' ' as const, text }));
  const tail = a.slice(endA).map((text) => ({ kind: ' ' as const, text }));
  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  return [...head, ...middle(midA, midB), ...tail];
}

function middle(a: string[], b: string[]): DiffLine[] {
  const n = a.length;
  const m = b.length;
  if ((n + 1) * (m + 1) > maxCells) {
    return [...a.map((text) => ({ kind: '-' as const, text })), ...b.map((text) => ({ kind: '+' as const, text }))];
  }
  const width = m + 1;
  const table = new Int32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      table[i * width + j] =
        a[i] === b[j] ? table[(i + 1) * width + j + 1] + 1 : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ kind: ' ', text: a[i] });
      i++;
      j++;
    } else if (table[(i + 1) * width + j] >= table[i * width + j + 1]) {
      out.push({ kind: '-', text: a[i++] });
    } else {
      out.push({ kind: '+', text: b[j++] });
    }
  }
  while (i < n) out.push({ kind: '-', text: a[i++] });
  while (j < m) out.push({ kind: '+', text: b[j++] });
  return out;
}

/** 只保留改动行前后 context 行，其余折叠成一行省略标记。 */
export function hunks(lines: DiffLine[], context = 3): (DiffLine | { kind: '…'; count: number })[] {
  const keep = new Array(lines.length).fill(false);
  lines.forEach((line, index) => {
    if (line.kind === ' ') return;
    for (let k = Math.max(0, index - context); k <= Math.min(lines.length - 1, index + context); k++) keep[k] = true;
  });
  const out: (DiffLine | { kind: '…'; count: number })[] = [];
  let skipped = 0;
  lines.forEach((line, index) => {
    if (keep[index]) {
      if (skipped) out.push({ kind: '…', count: skipped });
      skipped = 0;
      out.push(line);
    } else {
      skipped++;
    }
  });
  if (skipped) out.push({ kind: '…', count: skipped });
  return out;
}
