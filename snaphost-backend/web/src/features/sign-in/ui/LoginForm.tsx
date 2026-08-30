import styles from './LoginForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type SubmitHandler } from 'react-hook-form';
import { useLocation, useNavigate } from 'react-router-dom';
import { z } from 'zod';

import { signIn, useSessionDispatch } from '@/entities/session';

/**
 * The address is a login identifier, not a mailbox.
 *
 * This used to be `z.string().email()`, which rejected the account the
 * platform creates for itself: the default OPERATOR_EMAIL is
 * `operator@localhost`, and zod's email rule requires a dotted domain with a
 * top-level domain. The form refused to submit and react-hook-form put the
 * cursor back in the email field, so the panel would not accept the only
 * credential it had issued.
 *
 * Nothing here sends mail, and the server applies no format rule at all — it
 * lowercases the value and looks it up. A validator stricter than the server's
 * can only reject accounts that exist.
 */
const loginSchema = z.object({
  email: z.string().trim().min(1, 'Введите email.'),
  password: z.string().min(1, 'Введите пароль.'),
});

type LoginFormValues = z.infer<typeof loginSchema>;

interface LocationState {
  from?: string;
}

/**
 * The only way in.
 *
 * What went with the identity provider: a "continue with GitHub" button, the
 * divider under it, and a "forgot your password?" link. There is no OAuth to
 * redirect to and no mail to send a reset through — an operator who has lost
 * the password changes it against the database, which is a procedure in the
 * runbook rather than a screen.
 */
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

  return (
    <form onSubmit={handleSubmit(onSubmit)} className={`${styles.root} auth-form`} noValidate>
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
          autoComplete="username"
          {...register('email')}
          placeholder="operator@localhost"
        />
        <span aria-hidden="true">@</span>
      </div>
      {errors.email && <p className="auth-field-error">{errors.email.message}</p>}

      <label htmlFor="password">Пароль</label>
      <div className="auth-input">
        <input
          id="password"
          type="password"
          autoComplete="current-password"
          {...register('password')}
        />
        <span aria-hidden="true">••</span>
      </div>
      {errors.password && <p className="auth-field-error">{errors.password.message}</p>}

      <button type="submit" disabled={isSubmitting} className="auth-submit">
        {isSubmitting ? 'Входим…' : 'Войти'} <span aria-hidden="true">→</span>
      </button>
    </form>
  );
}

export default LoginForm;
