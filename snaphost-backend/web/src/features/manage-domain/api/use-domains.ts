import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { AttachDomainRequest, CustomDomain, DomainListResponse } from '@/entities/domain';
import { api, ApiError } from '@/shared/api/http';

/** A domain waiting on DNS is re-checked by the backend on its own ticker;
 *  poll only while something can still change on its own. */
const PENDING_POLL_MS = 10_000;
const RATE_LIMIT_BACKOFF_MS = 60_000;

export function useDomains() {
  return useQuery<DomainListResponse, Error>({
    queryKey: ['domains'],
    queryFn: () => api.get<DomainListResponse>('/api/v1/domains'),
    refetchInterval: (query) => {
      const error = query.state.error;
      if (error instanceof ApiError && error.status === 429) {
        return Math.max(error.retryAfterMs ?? 0, RATE_LIMIT_BACKOFF_MS);
      }
      const domains = query.state.data?.domains;
      if (!Array.isArray(domains)) return false;
      const awaitingDns = domains.some((d) => d.status === 'pending' || d.status === 'failed');
      return awaitingDns ? PENDING_POLL_MS : false;
    },
    refetchOnWindowFocus: true,
  });
}

export function useAttachDomain() {
  const queryClient = useQueryClient();
  return useMutation<CustomDomain, Error, AttachDomainRequest>({
    mutationFn: (input) => api.post<CustomDomain>('/api/v1/domains', input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

export function useDetachDomain() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => api.delete<void>(`/api/v1/domains/${id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

/** Publishing and rollback are the same call: move the pointer to another
 *  running deploy of the same project. Nothing is rebuilt. */
export function useRepointDomain() {
  const queryClient = useQueryClient();
  return useMutation<CustomDomain, Error, { id: string; deployId: string }>({
    mutationFn: ({ id, deployId }) =>
      api.post<CustomDomain>(`/api/v1/domains/${id}/target`, { deploy_id: deployId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}
