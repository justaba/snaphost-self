import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ApiKeyListResponse, CreateApiKeyResponse } from '@/entities/api-key';
import { api } from '@/shared/api/http';

export function useApiKeys() {
  return useQuery<ApiKeyListResponse, Error>({
    queryKey: ['apiKeys'],
    queryFn: () => api.get<ApiKeyListResponse>('/api/v1/keys'),
  });
}

export function useCreateApiKey() {
  const queryClient = useQueryClient();
  return useMutation<CreateApiKeyResponse, Error, { name?: string }>({
    mutationFn: (input) => api.post<CreateApiKeyResponse>('/api/v1/keys', input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
    },
  });
}

export function useRevokeApiKey() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => api.delete<void>(`/api/v1/keys/${id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
    },
  });
}
