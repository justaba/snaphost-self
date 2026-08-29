interface ApiAuthSession {
  accessToken: string;
}

interface ApiAuthBridge {
  getSession: () => Promise<ApiAuthSession | null>;
  refreshSession: () => Promise<ApiAuthSession | null>;
  signOut: () => Promise<void>;
}

let authBridge: ApiAuthBridge | null = null;

export function configureApiAuth(bridge: ApiAuthBridge): void {
  authBridge = bridge;
}

class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
    message: string,
    public readonly retryAfterMs?: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const baseURL = import.meta.env.VITE_API_URL;
if (!baseURL) throw new Error('VITE_API_URL must be set');

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;

  const seconds = Number(value);
  if (Number.isFinite(seconds)) return Math.max(0, seconds * 1000);

  const retryAt = Date.parse(value);
  if (!Number.isNaN(retryAt)) return Math.max(0, retryAt - Date.now());

  return undefined;
}

async function request<T>(
  path: string,
  init: RequestInit = {},
  extraHeaders?: Record<string, string>,
  isRetry = false,
): Promise<T> {
  if (!authBridge) throw new Error('API auth bridge is not configured');
  const session = await authBridge.getSession();

  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json');
  if (session) headers.set('Authorization', `Bearer ${session.accessToken}`);
  if (extraHeaders) {
    for (const [key, value] of Object.entries(extraHeaders)) {
      headers.set(key, value);
    }
  }

  const response = await fetch(`${baseURL}${path}`, { ...init, headers });

  if (response.status === 401 && !isRetry && session) {
    const refreshed = await authBridge.refreshSession();
    if (refreshed) {
      return request<T>(path, init, extraHeaders, true);
    }
    await authBridge.signOut();
    throw new ApiError(401, null, 'Session expired');
  }

  if (!response.ok) {
    let body: unknown = null;
    try {
      body = await response.json();
    } catch {
      // response had no JSON body; leave body as null
    }
    throw new ApiError(
      response.status,
      body,
      `Request failed: ${response.status}`,
      parseRetryAfter(response.headers.get('Retry-After')),
    );
  }

  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  get: <T>(path: string, headers?: Record<string, string>): Promise<T> =>
    request<T>(path, { method: 'GET' }, headers),
  post: <T>(path: string, body?: unknown, headers?: Record<string, string>): Promise<T> =>
    request<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }, headers),
  put: <T>(path: string, body?: unknown, headers?: Record<string, string>): Promise<T> =>
    request<T>(path, { method: 'PUT', body: body ? JSON.stringify(body) : undefined }, headers),
  patch: <T>(path: string, body?: unknown, headers?: Record<string, string>): Promise<T> =>
    request<T>(path, { method: 'PATCH', body: body ? JSON.stringify(body) : undefined }, headers),
  delete: <T>(path: string, headers?: Record<string, string>): Promise<T> =>
    request<T>(path, { method: 'DELETE' }, headers),
};

export { ApiError };
