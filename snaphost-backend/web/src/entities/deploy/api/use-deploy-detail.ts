import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '@/shared/api/http';
import { isTransitionalStatus, type DeployDetail } from '../model/types';

const TRANSITION_POLL_MS = 5_000;
const RATE_LIMIT_BACKOFF_MS = 60_000;

export function useDeployDetail(id: string | null | undefined) {
  return useQuery<DeployDetail>({
    queryKey: ['deploys', id],
    queryFn: () => api.get<DeployDetail>(`/api/v1/deploys/${id}`),
    enabled: Boolean(id),
    refetchInterval: (query) => {
      const error = query.state.error;
      if (error instanceof ApiError && error.status === 429) {
        return Math.max(error.retryAfterMs ?? 0, RATE_LIMIT_BACKOFF_MS);
      }

      const data = query.state.data;
      if (!data || !data.status) return false;
      return isTransitionalStatus(data.status) ? TRANSITION_POLL_MS : false;
    },
    refetchOnWindowFocus: false,
  });
}

export default useDeployDetail;
