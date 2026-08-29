/** Types for the operator API (`/api/v1/admin/*`). They mirror the Go structs
 *  in `user-billing/internal/admin`; nothing here is user-facing. */

export interface AdminOverview {
  users: number;
  wallets: number;
  total_balance: number;
  total_reserved: number;
  deploys: number;
  deploys_running: number;
  deploys_failed: number;
  deploys_24h: number;
  projects: number;
  domains_pending: number;
  domains_verified: number;
  active_api_keys: number;
  coins_topped_up: number;
  coins_spent: number;
  /** Accounts with no wallet. Non-zero means the Supabase seed webhook is not
   *  firing and those users cannot deploy at all. */
  walletless_users: number;
}

export interface AdminUserSummary {
  id: string;
  email?: string;
  balance?: number;
  reserved?: number;
  has_wallet: boolean;
  deploys_total: number;
  deploys_running: number;
  deploys_failed: number;
  coins_topped_up: number;
  coins_spent: number;
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
  cost_vibecoins: number;
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
  coins_reserved: boolean;
  image_built: boolean;
  container_running: boolean;
  coins_committed: boolean;
  retry_count: number;
  failure_reason?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface AdminLedgerEntry {
  id: string;
  user_id: string;
  user_email?: string;
  deploy_id?: string;
  type: 'reserve' | 'commit' | 'refund' | 'topup' | 'bonus';
  amount: number;
  status: string;
  created_at: string;
  completed_at?: string;
}

export interface AdminDeployDetail extends AdminDeploy {
  saga?: AdminSaga;
  domains: AdminDomain[];
  ledger: AdminLedgerEntry[];
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
