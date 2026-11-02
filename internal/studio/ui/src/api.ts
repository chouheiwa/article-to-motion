// Studio 接口的薄封装。错误统一抛 ApiError，message 用后端给的中文说明。

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly body: Record<string, unknown>,
  ) {
    super(message);
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  let body: Record<string, unknown> = {};
  try {
    body = await response.json();
  } catch {
    // 非 JSON 响应（例如 404 页面）按空对象处理，下面按状态码报错。
  }
  if (!response.ok) {
    const message = typeof body.message === 'string' ? body.message : `请求失败（${response.status}）`;
    throw new ApiError(message, response.status, String(body.code ?? ''), body);
  }
  return body as T;
}

export function post<T>(path: string, payload: unknown = {}): Promise<T> {
  return api<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

/** 项目内文件的原始地址（视频、图片、音频）。v 用来在文件变化后绕过缓存。 */
export function rawURL(path: string, version?: number): string {
  const query = new URLSearchParams({ path });
  if (version) query.set('v', String(version));
  return `/api/raw?${query}`;
}

export function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
