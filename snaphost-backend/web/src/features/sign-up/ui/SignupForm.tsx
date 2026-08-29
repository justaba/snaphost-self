import styles from './SignupForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type SubmitHandler } from 'react-hook-form';
import { Link, useNavigate } from 'react-router-dom';
import { z } from 'zod';

import { signInWithOAuth, signUp, useSessionDispatch } from '@/entities/session';
import { LEGAL_VERSION } from '@/shared/config/legal';

const signupSchema = z
  .object({
    displayName: z.string().optional(),
    email: z.string().email('Введите корректный email.'),
    password: z.string().min(8, 'Пароль должен содержать не менее 8 символов.'),
    passwordConfirm: z.string(),
    githubUsername: z.string().optional(),
    acceptTerms: z.boolean().refine(Boolean, 'Подтвердите принятие оферты и правил.'),
    personalDataConsent: z
      .boolean()
      .refine(Boolean, 'Для регистрации необходимо согласие на обработку данных.'),
  })
  .refine((data) => data.password === data.passwordConfirm, {
    message: 'Пароли не совпадают.',
    path: ['passwordConfirm'],
  });

type SignupFormValues = z.infer<typeof signupSchema>;

export function SignupForm() {
  const dispatch = useSessionDispatch();
  const navigate = useNavigate();
  const [toast, setToast] = useState<string | null>(null);
  const [confirmationEmail, setConfirmationEmail] = useState<string | null>(null);
  const [isGithubLoading, setIsGithubLoading] = useState(false);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<SignupFormValues>({
    resolver: zodResolver(signupSchema),
    defaultValues: {
      acceptTerms: false,
      personalDataConsent: false,
    },
  });

  const onSubmit: SubmitHandler<SignupFormValues> = async (values) => {
    setToast(null);
    const result = await dispatch(
      signUp({
        email: values.email,
        password: values.password,
        displayName: values.displayName || undefined,
        githubUsername: values.githubUsername || undefined,
        offerAccepted: values.acceptTerms,
        acceptableUseAccepted: values.acceptTerms,
        personalDataConsent: values.personalDataConsent,
        ageConfirmed: values.acceptTerms,
        legalVersion: LEGAL_VERSION,
        acceptedAt: new Date().toISOString(),
      }),
    );

    if (signUp.fulfilled.match(result)) {
      if (result.payload) {
        navigate('/dashboard', { replace: true });
      } else {
        setConfirmationEmail(values.email);
      }
    } else {
      setToast(result.payload?.message ?? 'Не удалось создать аккаунт. Попробуйте ещё раз.');
    }
  };

  const onGithub = async (): Promise<void> => {
    setToast(null);
    setIsGithubLoading(true);
    const result = await dispatch(
      signInWithOAuth({
        provider: 'github',
        redirectTo: `${window.location.origin}/auth/callback`,
      }),
    );

    if (signInWithOAuth.rejected.match(result)) {
      setToast(result.payload?.message ?? 'Не удалось начать регистрацию через GitHub.');
      setIsGithubLoading(false);
    }
  };

  if (confirmationEmail) {
    return (
      <div className={`${styles.root} auth-confirmation`} role="status">
        <span className="auth-confirmation-mark">✓</span>
        <h3>Проверьте почту</h3>
        <p>
          Мы отправили ссылку для подтверждения на <strong>{confirmationEmail}</strong>. Перейдите
          по ней, чтобы активировать аккаунт.
        </p>
      </div>
    );
  }

  return (
    <div className={styles.root}>
      <button type="button" onClick={onGithub} className="github-login" disabled={isGithubLoading}>
        <span className="github-mark" aria-hidden="true">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 .5C5.648.5.5 5.648.5 12a11.5 11.5 0 0 0 7.862 10.915c.575.105.785-.25.785-.555 0-.274-.01-1-.015-1.963-3.197.695-3.872-1.542-3.872-1.542-.523-1.328-1.277-1.682-1.277-1.682-1.044-.714.08-.7.08-.7 1.155.082 1.763 1.186 1.763 1.186 1.026 1.757 2.693 1.25 3.35.955.104-.744.402-1.25.73-1.538-2.552-.29-5.236-1.276-5.236-5.679 0-1.254.448-2.28 1.184-3.084-.118-.29-.513-1.46.113-3.043 0 0 .966-.31 3.164 1.178a10.97 10.97 0 0 1 2.88-.388c.977.005 1.96.132 2.88.388 2.196-1.488 3.16-1.178 3.16-1.178.628 1.583.233 2.753.115 3.043.737.804 1.183 1.83 1.183 3.084 0 4.414-2.689 5.386-5.25 5.671.413.355.78 1.056.78 2.13 0 1.537-.014 2.775-.014 3.152 0 .307.207.665.79.552A11.5 11.5 0 0 0 23.5 12C23.5 5.648 18.352.5 12 .5Z" />
          </svg>
        </span>
        {isGithubLoading ? 'Переходим в GitHub…' : 'Продолжить с GitHub'}
        <span aria-hidden="true">↗</span>
      </button>

      <div className="auth-divider">
        <span>или с помощью почты</span>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="auth-form auth-signup-form" noValidate>
        {toast && (
          <div role="alert" className="auth-message auth-message-error">
            {toast}
          </div>
        )}

        <label htmlFor="signup-display-name">
          Имя <span>(необязательно)</span>
        </label>
        <div className="auth-input">
          <input
            id="signup-display-name"
            type="text"
            autoComplete="name"
            {...register('displayName')}
            placeholder="Алексей"
          />
        </div>

        <label htmlFor="signup-email">Email</label>
        <div className="auth-input">
          <input
            id="signup-email"
            type="email"
            autoComplete="email"
            {...register('email')}
            placeholder="name@company.ru"
          />
          <span aria-hidden="true">@</span>
        </div>
        {errors.email && <p className="auth-field-error">{errors.email.message}</p>}

        <label htmlFor="signup-password">Пароль</label>
        <div className="auth-input">
          <input
            id="signup-password"
            type="password"
            autoComplete="new-password"
            {...register('password')}
            placeholder="Не менее 8 символов"
          />
          <span aria-hidden="true">••</span>
        </div>
        {errors.password && <p className="auth-field-error">{errors.password.message}</p>}

        <label htmlFor="signup-password-confirm">Повторите пароль</label>
        <div className="auth-input">
          <input
            id="signup-password-confirm"
            type="password"
            autoComplete="new-password"
            {...register('passwordConfirm')}
            placeholder="Повторите пароль"
          />
          <span aria-hidden="true">••</span>
        </div>
        {errors.passwordConfirm && (
          <p className="auth-field-error">{errors.passwordConfirm.message}</p>
        )}

        <label htmlFor="signup-github-username">
          GitHub <span>(необязательно)</span>
        </label>
        <div className="auth-input">
          <input
            id="signup-github-username"
            type="text"
            autoComplete="off"
            {...register('githubUsername')}
            placeholder="octocat"
          />
        </div>

        <div className="auth-consents">
          <label className="auth-consent">
            <input type="checkbox" {...register('acceptTerms')} />
            <span>
              Мне исполнилось 18 лет, я принимаю{' '}
              <Link to="/legal/offer" target="_blank">
                Публичную оферту
              </Link>{' '}
              и{' '}
              <Link to="/legal/acceptable-use" target="_blank">
                Правила допустимого использования
              </Link>
              .
            </span>
          </label>
          {errors.acceptTerms && <p className="auth-field-error">{errors.acceptTerms.message}</p>}

          <label className="auth-consent">
            <input type="checkbox" {...register('personalDataConsent')} />
            <span>
              Я даю{' '}
              <Link to="/legal/consent" target="_blank">
                согласие на обработку персональных данных
              </Link>{' '}
              и ознакомился с{' '}
              <Link to="/legal/privacy" target="_blank">
                Политикой обработки персональных данных
              </Link>
              .
            </span>
          </label>
          {errors.personalDataConsent && (
            <p className="auth-field-error">{errors.personalDataConsent.message}</p>
          )}
        </div>

        <button type="submit" disabled={isSubmitting} className="auth-submit">
          {isSubmitting ? 'Создаём аккаунт…' : 'Создать аккаунт'} <span aria-hidden="true">→</span>
        </button>
      </form>
    </div>
  );
}

export default SignupForm;
