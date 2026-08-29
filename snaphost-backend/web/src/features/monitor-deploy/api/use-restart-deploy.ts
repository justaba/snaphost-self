import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { CreateDeployResponse } from '@/entities/deploy';
import { api } from '@/shared/api/http';

export function useRestartDeploy() {
  const queryClient = useQueryClient();

  return useMutation<CreateDeployResponse, Error, string>({
    mutationFn: (id) => {
      const idempotencyKey = crypto.randomUUID();
      return api.post<CreateDeployResponse>(`/api/v1/deploys/${id}/restart`, undefined, {
        'Idempotency-Key': idempotencyKey,
      });
    },
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ['deploys'] });
      void queryClient.invalidateQueries({ queryKey: ['deploys', id] });
      void queryClient.invalidateQueries({ queryKey: ['billing'] });
    },
  });
}

export default useRestartDeploy;
