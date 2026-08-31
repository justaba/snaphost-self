import { useMutation, useQueryClient } from '@tanstack/react-query';

import { apiErrorMessage } from '@/shared/lib/api-error-message';
import { api } from '@/shared/api/http';

interface StartResponse {
  status: string;
  container_id: string;
  endpoint_url: string;
}

function invalidateEverything(queryClient: ReturnType<typeof useQueryClient>) {
  void queryClient.invalidateQueries({ queryKey: ['deploys'] });
  void queryClient.invalidateQueries({ queryKey: ['projects'] });
}

/** Starting a stopped deploy re-runs the image it was already built into. It
 *  is not a rebuild: the artifact is on disk, the port the build detected is
 *  on the saga row, and the runtime still probes the port before reporting
 *  success — so this takes seconds where a redeploy takes minutes.
 *
 *  It only works while that image exists. The watchdog reclaims images for
 *  failed and deleted deploys and spares stopped ones for exactly this
 *  reason, but retention eventually deletes an old deploy and takes its image
 *  with it — hence image_reclaimed, which means "deploy the project again". */
export function useStartDeploy() {
  const queryClient = useQueryClient();

  return useMutation<StartResponse, Error, string>({
    mutationFn: (id) => api.post<StartResponse>(`/api/v1/deploys/${id}/start`),
    onSuccess: () => invalidateEverything(queryClient),
  });
}

/** Stopping tears the container down and keeps the image, so the deploy can be
 *  started again.
 *
 *  It has its own endpoint, and that is the point. "Остановить" used to call
 *  DELETE /deploys/:id — which marks the deploy deleted and releases its image
 *  — so pausing a site destroyed the artifact Start needs. The two buttons
 *  were the same request with different labels. */
export function useStopDeploy() {
  const queryClient = useQueryClient();

  return useMutation<{ status: string }, Error, string>({
    mutationFn: (id) => api.post<{ status: string }>(`/api/v1/deploys/${id}/stop`),
    onSuccess: () => invalidateEverything(queryClient),
  });
}

export function stopDeployMessage(error: Error): string {
  return apiErrorMessage(error, 'Не удалось остановить деплой', {
    not_running: 'Деплой уже не запущен',
    stop_failed: 'Docker не смог остановить контейнер',
    runner_unavailable: 'Runtime не настроен',
  });
}

export function startDeployMessage(error: Error): string {
  return apiErrorMessage(error, 'Не удалось запустить деплой', {
    image_reclaimed: 'Образ сборки уже удалён — задеплойте проект заново',
    not_stopped: 'Деплой не остановлен',
    start_failed: 'Контейнер не поднялся. Откройте логи деплоя',
    runner_unavailable: 'Runtime не настроен',
    forbidden: 'Деплой принадлежит другому аккаунту',
  });
}
