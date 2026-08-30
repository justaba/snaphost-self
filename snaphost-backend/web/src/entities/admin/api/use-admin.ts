import { keepPreviousData, useQuery } from '@tanstack/react-query';

import { api } from '@/shared/api/http';
import type {
  AdminApiKey,
  AdminDeploy,
  AdminDeployDetail,
  AdminDomain,
  AdminListQuery,
  AdminOverview,
  AdminPage,
  AdminUserDetail,
  AdminUserSummary,
} from '../model/types';

/** Operator screens are read on demand rather than watched, so nothing here
 *  polls. The one exception would be a live incident view, which does not
 *  exist yet. */
const STALE_MS = 15_000;

function toSearch(query: AdminListQuery): string {
  const params = new URLSearchParams();
  if (query.q) params.set('q', query.q);
  if (query.status) params.set('status', query.status);
  if (query.type) params.set('type', query.type);
  if (query.userId) params.set('user_id', query.userId);
  if (query.limit != null) params.set('limit', String(query.limit));
  if (query.offset) params.set('offset', String(query.offset));
  const search = params.toString();
  return search ? `?${search}` : '';
}

export function useAdminOverview() {
  return useQuery<AdminOverview, Error>({
    queryKey: ['admin', 'overview'],
    queryFn: () => api.get<AdminOverview>('/api/v1/admin/overview'),
    staleTime: STALE_MS,
  });
}

export function useAdminUsers(query: AdminListQuery) {
  return useQuery<AdminPage<AdminUserSummary>, Error>({
    queryKey: ['admin', 'users', query],
    queryFn: () => api.get<AdminPage<AdminUserSummary>>(`/api/v1/admin/users${toSearch(query)}`),
    staleTime: STALE_MS,
    // Paging without this blanks the table on every page change.
    placeholderData: keepPreviousData,
  });
}

export function useAdminUser(userId: string | undefined) {
  return useQuery<AdminUserDetail, Error>({
    queryKey: ['admin', 'user', userId],
    queryFn: () => api.get<AdminUserDetail>(`/api/v1/admin/users/${userId}`),
    enabled: Boolean(userId),
    staleTime: STALE_MS,
  });
}

export function useAdminDeploys(query: AdminListQuery) {
  return useQuery<AdminPage<AdminDeploy>, Error>({
    queryKey: ['admin', 'deploys', query],
    queryFn: () => api.get<AdminPage<AdminDeploy>>(`/api/v1/admin/deploys${toSearch(query)}`),
    staleTime: STALE_MS,
    placeholderData: keepPreviousData,
  });
}

export function useAdminDeploy(deployId: string | undefined) {
  return useQuery<AdminDeployDetail, Error>({
    queryKey: ['admin', 'deploy', deployId],
    queryFn: () => api.get<AdminDeployDetail>(`/api/v1/admin/deploys/${deployId}`),
    enabled: Boolean(deployId),
    staleTime: STALE_MS,
  });
}

export function useAdminDomains(query: AdminListQuery) {
  return useQuery<AdminPage<AdminDomain>, Error>({
    queryKey: ['admin', 'domains', query],
    queryFn: () => api.get<AdminPage<AdminDomain>>(`/api/v1/admin/domains${toSearch(query)}`),
    staleTime: STALE_MS,
    placeholderData: keepPreviousData,
  });
}

export function useAdminUserKeys(userId: string | undefined) {
  return useQuery<{ items: AdminApiKey[] }, Error>({
    queryKey: ['admin', 'user-keys', userId],
    queryFn: () => api.get<{ items: AdminApiKey[] }>(`/api/v1/admin/users/${userId}/keys`),
    enabled: Boolean(userId),
    staleTime: STALE_MS,
  });
}
