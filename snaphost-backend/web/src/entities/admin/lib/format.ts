/** Formatting shared by the operator screens. */

import type { Tone } from '@/shared/ui/status-pill';

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  return new Date(value).toLocaleString('ru-RU', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return '—';
  return new Date(value).toLocaleDateString('ru-RU', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
  });
}

export function formatCoins(value: number | null | undefined): string {
  if (value == null) return '—';
  return value.toLocaleString('ru-RU');
}

/** A UUID is unreadable in a table but has to stay copyable, so the cell shows
 *  the leading group and the full value goes in the title attribute. */
export function shortId(id: string | null | undefined): string {
  if (!id) return '—';
  return id.split('-')[0] ?? id.slice(0, 8);
}

const DEPLOY_STATUS_TONE: Record<string, Tone> = {
  running: 'green',
  pending: 'amber',
  reserved: 'amber',
  building: 'blue',
  provisioning: 'blue',
  failed: 'red',
  stopped: 'zinc',
  deleted: 'zinc',
  expired: 'zinc',
};

const DOMAIN_STATUS_TONE: Record<string, Tone> = {
  verified: 'green',
  pending: 'amber',
  failed: 'red',
  revoked: 'zinc',
};

/** A ledger type's tone reads as direction of money, not as health: coins
 *  arriving are green, coins leaving are zinc, a reserve still in flight is
 *  amber because it is the state that can get stuck. */
const LEDGER_TYPE_TONE: Record<string, Tone> = {
  topup: 'green',
  bonus: 'green',
  refund: 'blue',
  commit: 'zinc',
  reserve: 'amber',
};

export function deployTone(status: string): Tone {
  return DEPLOY_STATUS_TONE[status] ?? 'zinc';
}

export function domainTone(status: string): Tone {
  return DOMAIN_STATUS_TONE[status] ?? 'zinc';
}

export function ledgerTone(type: string): Tone {
  return LEDGER_TYPE_TONE[type] ?? 'zinc';
}

export const LEDGER_TYPE_LABEL: Record<string, string> = {
  topup: 'пополнение',
  bonus: 'бонус',
  reserve: 'резерв',
  commit: 'списание',
  refund: 'возврат',
};

export const SOURCE_TYPE_LABEL: Record<string, string> = {
  git_public: 'git',
  git_private: 'git (private)',
  archive: 'архив',
};
