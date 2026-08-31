import { useQuery } from '@tanstack/react-query';

import { api } from '@/shared/api/http';
import type { AuditList, ProjectSummaryList } from '../model/types';

/** Project rows are read on demand rather than watched — nothing here changes
 *  without the operator doing it. The deploy list is the thing that moves
 *  during a build, and it has its own polling. */
const STALE_MS = 10_000;

export function useProjects() {
  return useQuery<ProjectSummaryList, Error>({
    queryKey: ['projects'],
    queryFn: () => api.get<ProjectSummaryList>('/api/v1/projects'),
    staleTime: STALE_MS,
  });
}

export function useAuditLog(enabled = true) {
  return useQuery<AuditList, Error>({
    queryKey: ['audit'],
    queryFn: () => api.get<AuditList>('/api/v1/audit?limit=50'),
    enabled,
    staleTime: STALE_MS,
  });
}
