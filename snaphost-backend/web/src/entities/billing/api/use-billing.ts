import { useQuery } from '@tanstack/react-query';
import { api } from '@/shared/api/http';
import type { BillingResponse } from '../model/types';

export function useBilling() {
  return useQuery<BillingResponse>({
    queryKey: ['billing'],
    queryFn: () => api.get<BillingResponse>('/api/v1/billing'),
    staleTime: 10_000,
  });
}

export default useBilling;
