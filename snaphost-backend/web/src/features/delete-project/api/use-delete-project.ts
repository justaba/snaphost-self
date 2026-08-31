import { useMutation, useQueryClient } from '@tanstack/react-query';

import { api } from '@/shared/api/http';
import { apiErrorMessage } from '@/shared/lib/api-error-message';

/** Why this one is not optimistic, unlike deleting a deploy.
 *
 *  The server stops containers and removes images before it touches a single
 *  row, and it refuses outright while a build is in flight. So the two most
 *  likely outcomes are a 409 and a 502 — both of which mean the project is
 *  still there. Removing it from the table first and putting it back on the
 *  error would show the operator a deletion that did not happen. */
export function useDeleteProject() {
  const queryClient = useQueryClient();

  return useMutation<void, Error, string>({
    mutationFn: (projectId) => api.delete<void>(`/api/v1/projects/${projectId}`),
    onSuccess: () => {
      // Deleting a project also removes its deploys and domains, so every list
      // on the dashboard is stale, not only the project one.
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      void queryClient.invalidateQueries({ queryKey: ['deploys'] });
      void queryClient.invalidateQueries({ queryKey: ['domains'] });
      void queryClient.invalidateQueries({ queryKey: ['audit'] });
    },
  });
}

/** The server's refusals are specific and each one tells the operator to do a
 *  different thing, so they are worth more than "не удалось удалить". */
export function deleteProjectMessage(error: Error): string {
  return apiErrorMessage(error, 'Не удалось удалить проект', {
    project_busy: 'Идёт сборка или запуск — удаление возможно после её завершения',
    cleanup_failed:
      'Не удалось убрать контейнер или образ. Ничего не удалено, проверьте Docker и повторите',
    runtime_unavailable: 'Runtime не настроен, а у проекта есть контейнер или образ',
    project_not_found: 'Проект уже удалён',
  });
}

export default useDeleteProject;
