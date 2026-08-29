import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { DeployListResponse } from '@/entities/deploy';
import { api } from '@/shared/api/http';

interface DeleteContext {
  previousLists: Array<[readonly unknown[], DeployListResponse | undefined]>;
}

export function useDeleteDeploy() {
  const queryClient = useQueryClient();

  return useMutation<void, Error, string, DeleteContext>({
    mutationFn: (id) => api.delete<void>(`/api/v1/deploys/${id}`),
    onMutate: async (id) => {
      await queryClient.cancelQueries({ queryKey: ['deploys'] });
      const previousLists = queryClient.getQueriesData<DeployListResponse>({
        queryKey: ['deploys'],
      });

      previousLists.forEach(([key, data]) => {
        if (!data || !Array.isArray(data.deploys)) return;
        const filtered = data.deploys.filter((item) => item.id !== id);
        if (filtered.length === data.deploys.length) return;
        queryClient.setQueryData<DeployListResponse>(key, {
          ...data,
          deploys: filtered,
          total: Math.max(0, data.total - 1),
        });
      });

      return { previousLists };
    },
    onError: (_err, _id, context) => {
      context?.previousLists.forEach(([key, data]) => {
        if (data) queryClient.setQueryData(key, data);
      });
      void queryClient.invalidateQueries({ queryKey: ['deploys'] });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['deploys'] });
      void queryClient.invalidateQueries({ queryKey: ['billing'] });
    },
  });
}

export default useDeleteDeploy;
