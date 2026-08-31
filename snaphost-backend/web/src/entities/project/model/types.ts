/** One project as the Projects screen shows it.
 *
 *  running_count is the number the delete confirmation turns on: deleting a
 *  project with a running deploy takes a live site down, without one it only
 *  reclaims disk. stopped_count is what says the project can be brought back
 *  cheaply, because a stopped deploy keeps its image. */
export interface ProjectSummary {
  id: string;
  user_id: string;
  slug: string;
  source_key: string;
  deploys_count: number;
  running_count: number;
  stopped_count: number;
  domains_count: number;
  last_deploy_at?: string;
  created_at: string;
}

export interface ProjectSummaryList {
  items: ProjectSummary[];
  total: number;
}

/** One recorded destructive action. */
export interface AuditEntry {
  id: string;
  actor_user_id: string;
  action: string;
  target_type: string;
  target_id: string;
  details: Record<string, unknown>;
  created_at: string;
}

export interface AuditList {
  items: AuditEntry[];
  total: number;
}
