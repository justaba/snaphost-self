import { ApiError, type ApiErrorBody } from '@/shared/api/http';
import type { CustomDomain, DomainStatus } from './types';

/**
 * Explanations for the attach-time refusals the backend can return. The
 * backend sends a stable code plus an English message; a user staring at a
 * form needs to know what to do next, not what the server called it.
 */
const ATTACH_ERRORS: Record<string, string> = {
  invalid_domain:
    'Это не похоже на имя домена. Укажите его без протокола и пути — например, example.com.',
  wildcard_unsupported: 'Домены с «*» пока не поддерживаются. Привяжите конкретное имя.',
  reserved_domain: 'Это имя внутри нашего домена — его выдаёт платформа, привязать нельзя.',
  domain_taken:
    'Этот домен уже привязан. Отвяжите его в аккаунте, где он используется, или напишите в поддержку, если домен ваш.',
  domain_limit_reached: 'Достигнут лимит доменов на аккаунте. Отвяжите один, чтобы добавить новый.',
  rate_limited: 'Слишком много попыток привязки. Подождите немного и повторите.',
  edge_address_unreserved:
    'Привязка доменов пока недоступна: адрес, на который нужно направить DNS, ещё не зафиксирован.',
  identity_required: 'Для привязки домена нужен подтверждённый способ оплаты.',
  project_not_found: 'Проект не найден в этом аккаунте.',
  deploy_not_found: 'Деплой не найден в этом аккаунте.',
  invalid_target: 'Указанный деплой не запущен или относится к другому проекту.',
  invalid_deploy_id: 'Некорректный идентификатор деплоя.',
  invalid_project_id: 'Некорректный идентификатор проекта.',
};

/** Turns any API failure into one sentence a user can act on. */
export function attachErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    const body = error.body as ApiErrorBody | null;
    const known = body?.error ? ATTACH_ERRORS[body.error] : undefined;
    if (known) return known;
    if (error.status === 429) return ATTACH_ERRORS.rate_limited;
  }
  return fallback;
}

export interface DomainStateView {
  /** Short label for the badge. */
  label: string;
  tone: 'pending' | 'ok' | 'error' | 'muted';
  /** What is happening and what, if anything, the user should do. */
  detail: string;
}

/**
 * `txt_not_found` means two different things depending on how long we have
 * been waiting, and the backend encodes that in the status: a fresh domain
 * stays `pending` while DNS propagates and only becomes `failed` once the
 * grace window has passed. Telling a two-minute-old attach that verification
 * failed is what made a working setup look broken, so the two cases get
 * different copy.
 */
const FAILED_TXT_NOT_FOUND =
  'TXT-запись так и не появилась. Проверьте, что она создана с именем из инструкции ниже и в той зоне, где обслуживается домен — у некоторых регистраторов запись нужно добавлять без имени домена в конце.';

const FAILURE_DETAILS: Record<string, string> = {
  txt_not_found:
    'TXT-запись ещё не видна. Обычно DNS расходится за несколько минут, но у некоторых регистраторов это занимает часы — проверка идёт автоматически.',
  txt_mismatch:
    'TXT-запись найдена, но её значение не совпадает. Скопируйте значение ниже целиком и замените им запись.',
  dns_lookup_failed:
    'DNS-сервер не ответил при проверке. Это временно, статус домена не менялся — следующая проверка пройдёт сама.',
  unpinned_idle:
    'Домен долго не получал запросов, и мы освободили деплой. Выберите деплой заново, чтобы сайт снова открывался.',
};

/** One place that decides what a domain row says, so the badge and the
 *  explanation below it can never disagree. */
export function describeDomain(domain: CustomDomain): DomainStateView {
  const failure = domain.last_error ? FAILURE_DETAILS[domain.last_error] : undefined;

  switch (domain.status) {
    case 'verified':
      if (!domain.target_deploy_id) {
        return {
          label: 'Не опубликован',
          tone: 'pending',
          detail:
            failure ??
            'Домен подтверждён, но не указывает ни на один деплой — сейчас он отвечает 404. Выберите деплой.',
        };
      }
      return {
        label: 'Активен',
        tone: 'ok',
        detail: 'Домен подтверждён и обслуживает выбранный деплой.',
      };
    case 'pending':
      return {
        label: 'Ждём DNS',
        tone: 'pending',
        detail:
          failure ??
          'Добавьте TXT-запись из инструкции ниже. Мы проверяем её автоматически, обновлять страницу не нужно.',
      };
    case 'failed':
      return {
        label: 'Проверка не прошла',
        tone: 'error',
        detail:
          domain.last_error === 'txt_not_found'
            ? FAILED_TXT_NOT_FOUND
            : (failure ?? 'Не удалось подтвердить владение доменом. Проверьте TXT-запись.'),
      };
    case 'revoked':
      return {
        label: 'Отвязан',
        tone: 'muted',
        detail: 'Домен отвязан и больше не обслуживается.',
      };
  }
}

const TONE_CLASSES: Record<DomainStateView['tone'], string> = {
  ok: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  pending: 'bg-amber-50 text-amber-700 border-amber-200',
  error: 'bg-red-50 text-red-700 border-red-200',
  muted: 'bg-zinc-100 text-zinc-600 border-zinc-200',
};

export function toneClasses(tone: DomainStateView['tone']): string {
  return TONE_CLASSES[tone];
}

/** Whether the backend has published an address to point DNS at (Task 16 P2). */
export function hasPublishedTarget(domain: CustomDomain): boolean {
  return Boolean(domain.dns.cname_target || domain.dns.a_record_target);
}

export const DOMAIN_STATUS_ORDER: DomainStatus[] = ['failed', 'pending', 'verified', 'revoked'];
