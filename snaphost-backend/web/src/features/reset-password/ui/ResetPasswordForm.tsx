import styles from './ResetPasswordForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type SubmitHandler } from 'react-hook-form';
import { useNavigate } from 'react-router-dom';
import { z } from 'zod';

import { auth } from '@/entities/session';

const resetPasswordSchema = z
  .object({
    password: z.string().min(8, 'Пароль должен содержать не менее 8 символов.'),
    passwordConfirm: z.string(),
  })
  .refine(({ password, passwordConfirm }) => password === passwordConfirm, {
    message: 'Пароли не совпадают.',
    path: ['passwordConfirm'],
  });

type ResetPasswordFormValues = z.infer<typeof resetPasswordSchema>;

export function ResetPasswordForm() {
  const navigate = useNavigate();
  const [message, setMessage] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<ResetPasswordFormValues>({ resolver: zodResolver(resetPasswordSchema) });

  const onSubmit: SubmitHandler<ResetPasswordFormValues> = async ({ password }) => {
    setMessage(null);

    try {
      await auth.updatePassword(password);
      navigate('/dashboard', { replace: true });
    } catch {
      setMessage('Ссылка недействительна или устарела. Запросите новую ссылку восстановления.');
    }
  };

  return (
    <form className={`${styles.root} auth-form`} onSubmit={handleSubmit(onSubmit)} noValidate>
      {message && (
        <div className="auth-message auth-message-error" role="alert">
          {message}
        </div>
      )}

      <label htmlFor="new-password">Новый пароль</label>
      <div className="auth-input">
        <input
          id="new-password"
          type="password"
          autoComplete="new-password"
          placeholder="Не менее 8 символов"
          {...register('password')}
        />
        <span aria-hidden="true">••</span>
      </div>
      {errors.password && <p className="auth-field-error">{errors.password.message}</p>}

      <label htmlFor="new-password-confirm">Повторите пароль</label>
      <div className="auth-input">
        <input
          id="new-password-confirm"
          type="password"
          autoComplete="new-password"
          placeholder="Повторите новый пароль"
          {...register('passwordConfirm')}
        />
        <span aria-hidden="true">••</span>
      </div>
      {errors.passwordConfirm && (
        <p className="auth-field-error">{errors.passwordConfirm.message}</p>
      )}

      <button className="auth-submit" type="submit" disabled={isSubmitting}>
        {isSubmitting ? 'Сохраняем…' : 'Сохранить пароль'} <span aria-hidden="true">→</span>
      </button>
    </form>
  );
}
