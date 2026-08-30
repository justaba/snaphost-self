/**
 * The API client.
 *
 * It used to hold an access token, put it in an Authorization header, and on a
 * 401 refresh the token and retry once. All three are gone with the token: the
 * session is an HttpOnly cookie, so the browser attaches it and this code
 * cannot read it, and the server slides its expiry on use rather than handing
 * out a refresh token to exchange.
 *
 * What survives is the part that mattered — a 401 means the session is over,
 * and something has to notice. That is now one call to the sign-out bridge
 * instead of a retry loop.
 */

interface ApiAuthBridge {
  /** Called when the server says the session is no longer valid. */
  onUnauthenticated: () => void;
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

/**
 * Empty in production: one binary serves the panel and the API, so every
 * request is same-origin and a base URL would only be a way to get it wrong.
 * It stays configurable for a development server pointed at a backend
 * elsewhere.
 */
const baseURL = import.meta.env.VITE_API_URL ?? '';

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
): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json');
  if (extraHeaders) {
    for (const [key, value] of Object.entries(extraHeaders)) {
      headers.set(key, value);
    }
  }

  const response = await fetch(`${baseURL}${path}`, {
    ...init,
    headers,
    // Explicit rather than relying on the same-origin default, so a
    // development server on another port behaves the same as production.
    credentials: 'include',
  });

  if (response.status === 401) {
    authBridge?.onUnauthenticated();
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
