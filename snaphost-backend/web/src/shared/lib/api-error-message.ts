import { ApiError } from '@/shared/api/http';

/** Translate a server refusal into the sentence that tells the operator what
 *  to do next.
 *
 *  The control plane answers destructive requests with a specific code —
 *  project_busy, cleanup_failed, image_reclaimed — and each one implies a
 *  different action: wait for the build, fix the daemon, deploy again. A single
 *  "не удалось" for all of them throws that away, which on the screens that
 *  stop containers and delete images is the difference between an operator
 *  knowing whether anything happened.
 *
 *  It lives in shared because more than one feature needs it and features on
 *  the same layer may not import each other. */
export function apiErrorMessage(
  error: Error,
  fallback: string,
  messages: Record<string, string>,
): string {
  if (!(error instanceof ApiError)) return error.message || fallback;

  const code =
    typeof error.body === 'object' && error.body !== null
      ? (error.body as { error?: string }).error
      : undefined;

  return (code && messages[code]) ?? fallback;
}

export default apiErrorMessage;
