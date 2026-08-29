import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '@/shared/api/http';
import { isTransitionalStatus, type DeployListResponse, type DeployStatus } from '../model/types';

export interface UseDeploysParams {
  status?: DeployStatus;
  limit?: number;
  offset?: number;
}

const TRANSITION_POLL_MS = 15_000;
const RATE_LIMIT_BACKOFF_MS = 60_000;

export function useDeploys(params?: UseDeploysParams) {
  return useQuery<DeployListResponse>({
    queryKey: ['deploys', params],
    queryFn: () => {
      const search = new URLSearchParams();
      if (params?.status) search.set('status', params.status);
      if (params?.limit !== undefined) search.set('limit', String(params.limit));
      if (params?.offset !== undefined) search.set('offset', String(params.offset));
      const qs = search.toString();
      return api.get<DeployListResponse>(`/api/v1/deploys${qs ? `?${qs}` : ''}`);
    },
    refetchInterval: (query) => {
      const error = query.state.error;
      if (error instanceof ApiError && error.status === 429) {
        return Math.max(error.retryAfterMs ?? 0, RATE_LIMIT_BACKOFF_MS);
      }

      const data = query.state.data;
      if (!data || !Array.isArray(data.deploys)) return false;
      const inTransition = data.deploys.some((d) => isTransitionalStatus(d.status));
      return inTransition ? TRANSITION_POLL_MS : false;
    },
    refetchOnWindowFocus: false,
  });
}

export default useDeploys;
