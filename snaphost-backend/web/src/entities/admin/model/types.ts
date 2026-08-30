/** Types for the operator API (`/api/v1/admin/*`). They mirror the Go structs
 *  in `internal/control/admin`; nothing here is user-facing.
 *
 *  Every wallet, balance and coin field is gone with billing (Task 1 item 3).
 *  They were not merely unused: the server stopped sending them, so each one
 *  was a number the table rendered as undefined. */

export interface AdminOverview {
  users: number;
  deploys: number;
  deploys_running: number;
  deploys_failed: number;
  deploys_24h: number;
  projects: number;
  domains_pending: number;
  domains_verified: number;
  active_api_keys: number;
}

export interface AdminUserSummary {
  id: string;
  email?: string;
  deploys_total: number;
  deploys_running: number;
  deploys_failed: number;
  domains_count: number;
  last_deploy_at?: string;
  created_at: string;
}

export interface AdminProject {
  id: string;
  user_id: string;
  slug: string;
  source_key: string;
  deploys_count: number;
  created_at: string;
}

export interface AdminDomain {
  id: string;
  user_id: string;
  user_email?: string;
  project_id: string;
  target_deploy_id?: string;
  domain: string;
  status: 'pending' | 'verified' | 'failed' | 'revoked';
  last_error?: string;
  verified_at?: string;
  last_checked_at?: string;
  created_at: string;
}

export interface AdminApiKey {
  id: string;
  user_id: string;
  prefix: string;
  name: string;
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

export interface AdminUserDetail extends AdminUserSummary {
  projects: AdminProject[];
  domains: AdminDomain[];
  api_keys: AdminApiKey[];
}

export interface AdminDeploy {
  id: string;
  user_id: string;
  user_email?: string;
  project_id?: string;
  project_slug?: string;
  source_type: 'git_public' | 'git_private' | 'archive';
  repo_url: string;
  branch: string;
  commit_sha?: string;
  status: string;
  image_ref?: string;
  endpoint_url?: string;
  subdomain?: string;
  container_id?: string;
  failure_reason?: string;
  ttl_expires_at?: string;
  last_request_at?: string;
  created_at: string;
  updated_at: string;
  stopped_at?: string;
}

export interface AdminSaga {
  deploy_id: string;
  current_step: string;
  image_built: boolean;
  container_running: boolean;
  retry_count: number;
  failure_reason?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface AdminDeployDetail extends AdminDeploy {
  saga?: AdminSaga;
  domains: AdminDomain[];
}

export interface AdminPage<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface AdminListQuery {
  q?: string;
  status?: string;
  type?: string;
  userId?: string;
  limit?: number;
  offset?: number;
}
