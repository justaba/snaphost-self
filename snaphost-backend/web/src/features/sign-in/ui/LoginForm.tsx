import styles from './LoginForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type SubmitHandler } from 'react-hook-form';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { z } from 'zod';

import { signIn, signInWithOAuth, useSessionDispatch } from '@/entities/session';

const loginSchema = z.object({
  email: z.string().email('Введите корректный email.'),
  password: z.string().min(1, 'Введите пароль.'),
});

type LoginFormValues = z.infer<typeof loginSchema>;

interface LocationState {
  from?: string;
}

export function LoginForm() {
  const dispatch = useSessionDispatch();
  const navigate = useNavigate();
  const location = useLocation();
  const [toast, setToast] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({ resolver: zodResolver(loginSchema) });

  const onSubmit: SubmitHandler<LoginFormValues> = async (values) => {
    setToast(null);
    const result = await dispatch(signIn(values));
    if (signIn.fulfilled.match(result)) {
      const state = (location.state ?? null) as LocationState | null;
      const redirectTo = state?.from ?? '/dashboard';
      navigate(redirectTo, { replace: true });
    } else {
      setToast(result.payload?.message ?? 'Не удалось войти. Проверьте данные и попробуйте снова.');
    }
  };

  const onGithub = async (): Promise<void> => {
    setToast(null);
    const result = await dispatch(
      signInWithOAuth({
        provider: 'github',
        redirectTo: `${window.location.origin}/auth/callback`,
      }),
    );
    if (signInWithOAuth.rejected.match(result)) {
      setToast(result.payload?.message ?? 'Не удалось начать вход через GitHub.');
    }
  };

  return (
    <form onSubmit={handleSubmit(onSubmit)} className={`${styles.root} auth-form`} noValidate>
      <button type="button" onClick={onGithub} className="github-login">
        <span className="github-mark" aria-hidden="true">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 .5C5.648.5.5 5.648.5 12a11.5 11.5 0 0 0 7.862 10.915c.575.105.785-.25.785-.555 0-.274-.01-1-.015-1.963-3.197.695-3.872-1.542-3.872-1.542-.523-1.328-1.277-1.682-1.277-1.682-1.044-.714.08-.7.08-.7 1.155.082 1.763 1.186 1.763 1.186 1.026 1.757 2.693 1.25 3.35.955.104-.744.402-1.25.73-1.538-2.552-.29-5.236-1.276-5.236-5.679 0-1.254.448-2.28 1.184-3.084-.118-.29-.513-1.46.113-3.043 0 0 .966-.31 3.164 1.178a10.97 10.97 0 0 1 2.88-.388c.977.005 1.96.132 2.88.388 2.196-1.488 3.16-1.178 3.16-1.178.628 1.583.233 2.753.115 3.043.737.804 1.183 1.83 1.183 3.084 0 4.414-2.689 5.386-5.25 5.671.413.355.78 1.056.78 2.13 0 1.537-.014 2.775-.014 3.152 0 .307.207.665.79.552A11.5 11.5 0 0 0 23.5 12C23.5 5.648 18.352.5 12 .5Z" />
          </svg>
        </span>
        Продолжить с GitHub
        <span aria-hidden="true">↗</span>
      </button>

      <div className="auth-divider">
        <span>или с помощью почты</span>
      </div>

      {toast && (
        <div role="alert" className="auth-message auth-message-error">
          {toast}
        </div>
      )}

      <label htmlFor="email">Email</label>
      <div className="auth-input">
        <input
          id="email"
          type="email"
          autoComplete="email"
          {...register('email')}
          placeholder="name@company.ru"
        />
        <span aria-hidden="true">@</span>
      </div>
      {errors.email && <p className="auth-field-error">{errors.email.message}</p>}

      <div className="auth-label-row">
        <label htmlFor="password">Пароль</label>
        <Link to="/recover">Забыли пароль?</Link>
      </div>
      <div className="auth-input">
        <input
          id="password"
          type="password"
          autoComplete="current-password"
          {...register('password')}
          placeholder="Не менее 8 символов"
        />
        <span aria-hidden="true">••</span>
      </div>
      {errors.password && <p className="auth-field-error">{errors.password.message}</p>}

      <button type="submit" disabled={isSubmitting} className="auth-submit">
        {isSubmitting ? 'Входим…' : 'Войти в Snaphost'} <span aria-hidden="true">→</span>
      </button>
    </form>
  );
}

export default LoginForm;
