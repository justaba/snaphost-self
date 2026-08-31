export type DeployStatus =
  | 'pending'
  | 'reserved'
  | 'building'
  | 'built'
  | 'provisioning'
  | 'running'
  | 'failed'
  | 'stopped'
  | 'deleted';

export type LogStage =
  | 'pipeline'
  | 'clone'
  | 'detect'
  | 'validate'
  | 'build'
  | 'scan'
  | 'runtime-startup'
  | 'runtime'
  | 'runtime-shutdown';

export type LogLevel = 'info' | 'warn' | 'error';

/** The orchestration state behind a deploy — what actually happened, as
 *  against `status`, which is what the operator is shown. It is the only
 *  answer to "why is this stuck": a saga at `compensating` with a retry count
 *  is a different problem from one that never left `pending`.
 *
 *  `current_step` is not a DeployStatus. The saga has `built` and
 *  `compensating`/`compensated` steps that are not deploy statuses, and typing
 *  it as one is what made this field look interchangeable with `status`. */
export interface DeploySaga {
  current_step: string;
  image_built: boolean;
  container_running: boolean;
  retry_count: number;
  app_port?: number;
  failure_reason?: string;
}

export interface DeploySummary {
  id: string;
  project_id: string | null;
  repo_url: string;
  branch: string;
  commit_sha: string | null;
  status: DeployStatus;
  endpoint_url: string | null;
  subdomain: string | null;
  ttl_expires_at: string | null;
  created_at: string;
  stopped_at: string | null;
}

export interface DeployDetail extends DeploySummary {
  user_id: string;
  image_ref: string | null;
  failure_reason: string | null;
  /** Absent for rows that predate the orchestrator, so the section is
   *  omitted rather than rendered empty. */
  saga?: DeploySaga;
  image_deleted_at?: string | null;
  container_id?: string | null;
  updated_at: string;
}

export interface CreateDeployRequest {
  repo_url: string;
  branch: string;
  env?: Record<string, string>;
  build_args?: Record<string, string>;
}

export interface CreateDeployResponse {
  deploy_id: string;
  status: DeployStatus;
  logs_channel: string;
  events_channel: string;
  poll_url: string;
  websocket_url: string;
}

export interface DeployListResponse {
  deploys: DeploySummary[];
  total: number;
  limit: number;
  offset: number;
}

export interface LogLine {
  deploy_id: string;
  stage: LogStage;
  level: LogLevel;
  text: string;
  timestamp: string;
}

export interface LogsHistoryEntry {
  id: string;
  line: LogLine;
}

export interface LogsHistoryResponse {
  entries: LogsHistoryEntry[];
  next_since: string | null;
}

export const TRANSITIONAL_STATUSES: DeployStatus[] = [
  'pending',
  'reserved',
  'building',
  'built',
  'provisioning',
];

export function isTransitionalStatus(status: DeployStatus): boolean {
  return TRANSITIONAL_STATUSES.includes(status);
}
