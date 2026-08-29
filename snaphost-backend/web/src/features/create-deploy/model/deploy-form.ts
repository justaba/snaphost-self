import { z } from 'zod';

import type { CreateDeployRequest } from '@/entities/deploy';
import { ApiError, type ApiErrorBody } from '@/shared/api/http';

const ALLOWED_HOSTS = new Set(['github.com', 'gitlab.com', 'bitbucket.org', 'www.github.com']);
const ENV_KEY_PATTERN = /^[A-Z_][A-Z0-9_]*$/;

const environmentEntrySchema = z.object({
  key: z
    .string()
    .trim()
    .min(1, 'Укажите имя переменной')
    .regex(ENV_KEY_PATTERN, 'Используйте заглавные латинские буквы, цифры и _')
    .refine((key) => !key.startsWith('SNAPHOST_'), 'Префикс SNAPHOST_ зарезервирован'),
  value: z.string(),
});

export const deployFormSchema = z
  .object({
    repo_url: z
      .string()
      .trim()
      .min(1, 'Укажите URL репозитория')
      .refine((value) => {
        try {
          const url = new URL(value);
          return url.protocol === 'https:' && ALLOWED_HOSTS.has(url.hostname.toLowerCase());
        } catch {
          return false;
        }
      }, 'Поддерживаются только https-адреса github.com, gitlab.com, bitbucket.org'),
    branch: z.string().trim().min(1, 'Укажите ветку').max(100, 'Максимум 100 символов'),
    env: z.array(environmentEntrySchema).max(50, 'Можно добавить не более 50 переменных'),
  })
  .superRefine(({ env }, context) => {
    const seen = new Set<string>();
    env.forEach(({ key }, index) => {
      if (seen.has(key)) {
        context.addIssue({
          code: z.ZodIssueCode.custom,
          message: 'Имена переменных не должны повторяться',
          path: ['env', index, 'key'],
        });
      }
      seen.add(key);
    });
  });

export type DeployFormValues = z.infer<typeof deployFormSchema>;

export interface DeployFormError {
  code: string;
  message: string;
}

export function toCreateDeployRequest(values: DeployFormValues): CreateDeployRequest {
  const env = Object.fromEntries(values.env.map(({ key, value }) => [key.trim(), value]));

  return {
    repo_url: values.repo_url.trim(),
    branch: values.branch.trim(),
    env: Object.keys(env).length > 0 ? env : undefined,
  };
}

export function extractDeployError(error: unknown): DeployFormError {
  if (error instanceof ApiError) {
    const body = error.body as ApiErrorBody | null;
    return {
      code: body?.error ?? 'unknown',
      message: body?.message ?? error.message,
    };
  }
  if (error instanceof Error) return { code: 'unknown', message: error.message };
  return { code: 'unknown', message: 'Не удалось создать деплой' };
}
