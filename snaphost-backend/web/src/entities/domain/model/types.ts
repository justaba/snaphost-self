export type DomainStatus = 'pending' | 'verified' | 'failed' | 'revoked';

export type DomainFailureCode =
  'txt_not_found' | 'txt_mismatch' | 'dns_lookup_failed' | 'unpinned_idle';

export interface DomainDnsInstructions {
  verification_record: string;
  verification_type: 'TXT';
  verification_value: string;
  cname_target?: string;
  a_record_target?: string;
  apex_note: string;
}

export interface CustomDomain {
  id: string;
  user_id: string;
  project_id: string;
  target_deploy_id: string | null;
  domain: string;
  verification_token: string;
  status: DomainStatus;
  last_error?: string | null;
  verified_at?: string | null;
  last_checked_at?: string | null;
  created_at: string;
  updated_at: string;
  dns: DomainDnsInstructions;
}

export interface DomainListResponse {
  domains: CustomDomain[];
  limit: number;
}

export interface AttachDomainRequest {
  domain: string;
  project_id?: string;
  deploy_id?: string;
}
