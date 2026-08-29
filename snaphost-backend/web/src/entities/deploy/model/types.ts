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

export interface DeploySaga {
  current_step: DeployStatus;
  coins_reserved: boolean;
  image_built: boolean;
  container_running: boolean;
  coins_committed: boolean;
  retry_count: number;
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
  cost_vibecoins: number;
  ttl_expires_at: string | null;
  created_at: string;
  stopped_at: string | null;
}

export interface DeployDetail extends DeploySummary {
  user_id: string;
  image_ref: string | null;
  failure_reason: string | null;
  saga: DeploySaga;
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
