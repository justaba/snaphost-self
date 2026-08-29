export interface ApiKeyInfo {
  id: string;
  prefix: string;
  name: string;
  created_at: string;
  last_used_at?: string | null;
}

export interface ApiKeyListResponse {
  keys: ApiKeyInfo[];
}

export interface CreateApiKeyResponse {
  id: string;
  key: string;
  prefix: string;
  name: string;
  created_at: string;
}
