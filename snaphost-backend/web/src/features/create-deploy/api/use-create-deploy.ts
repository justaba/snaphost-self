import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { CreateDeployRequest, CreateDeployResponse } from '@/entities/deploy';
import { api } from '@/shared/api/http';

export function useCreateDeploy() {
  const queryClient = useQueryClient();

  return useMutation<CreateDeployResponse, Error, CreateDeployRequest>({
    mutationFn: (input) => {
      const idempotencyKey = crypto.randomUUID();
      return api.post<CreateDeployResponse>('/api/v1/deploys', input, {
        'Idempotency-Key': idempotencyKey,
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['deploys'] });
    },
  });
}

export default useCreateDeploy;
