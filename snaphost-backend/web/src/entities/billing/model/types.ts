export interface BillingResponse {
  user_id: string;
  balance: number;
  reserved: number;
  currency: 'vibecoins';
  updated_at: string;
}
