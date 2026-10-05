import styles from './SetupForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { useNavigate } from 'react-router-dom';
import { z } from 'zod';

import { auth, sessionChanged, useSessionDispatch } from '@/entities/session';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';

const schema = z
  .object({
    email: z
      .string()
      .trim()
      .min(1, 'Введите логин.')
      .max(254, 'Логин слишком длинный.')
      .regex(/^\S+$/, 'Логин не должен содержать пробелы.'),
    password: z.string().min(12, 'Не менее 12 символов.').max(1024, 'Пароль слишком длинный.'),
    confirm: z.string(),
  })
  .refine((value) => value.password === value.confirm, {
    message: 'Пароли не совпадают.',
    path: ['confirm'],
  });

export function SetupForm({ token }: { token: string }) {
  const dispatch = useSessionDispatch();
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<z.infer<typeof schema>>({ resolver: zodResolver(schema) });

  if (!token) {
    return (
      <div className={styles.root}>
        <p className="text-sm text-zinc-700">
          Откройте одноразовую ссылку настройки, которую выдал установщик.
        </p>
        <p className="mt-3 text-xs text-zinc-500">
          По этой ссылке вы сможете задать свой логин и пароль.
        </p>
      </div>
    );
  }

  const submit = async ({ email, password }: z.infer<typeof schema>) => {
    setError(null);
    try {
      const session = await auth.completeSetup({ email, password, token });
      dispatch(sessionChanged(session));
      navigate('/dashboard', { replace: true });
    } catch (reason) {
      const message =
        reason && typeof reason === 'object' && 'message' in reason
          ? String(reason.message)
          : 'Не удалось создать аккаунт.';
      setError(message);
    }
  };

  return (
    <form
      className={`${styles.root} flex flex-col gap-4`}
      onSubmit={handleSubmit(submit)}
      noValidate
    >
      {error && (
        <p role="alert" className="text-sm text-red-700">
          {error}
        </p>
      )}
      <Input
        id="setup-login"
        label="Логин"
        autoComplete="username"
        placeholder="boris или boris@example.com"
        error={errors.email?.message}
        {...register('email')}
      />
      <Input
        id="setup-password"
        label="Пароль"
        type="password"
        autoComplete="new-password"
        hint="Не менее 12 символов."
        error={errors.password?.message}
        {...register('password')}
      />
      <Input
        id="setup-confirm"
        label="Повторите пароль"
        type="password"
        autoComplete="new-password"
        error={errors.confirm?.message}
        {...register('confirm')}
      />
      <Button type="submit" variant="primary" size="lg" loading={isSubmitting}>
        {isSubmitting ? 'Создаём аккаунт…' : 'Создать аккаунт'}
      </Button>
    </form>
  );
}
