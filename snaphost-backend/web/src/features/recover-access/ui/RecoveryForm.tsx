import styles from './RecoveryForm.module.css';

import { zodResolver } from '@hookform/resolvers/zod';
import { useState } from 'react';
import { useForm, type SubmitHandler } from 'react-hook-form';
import { Link } from 'react-router-dom';
import { z } from 'zod';

import { auth } from '@/entities/session';

const recoverySchema = z.object({
  email: z.string().email('Введите корректный email.'),
});

type RecoveryFormValues = z.infer<typeof recoverySchema>;

export function RecoveryForm() {
  const [message, setMessage] = useState<string | null>(null);
  const [hasError, setHasError] = useState(false);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<RecoveryFormValues>({ resolver: zodResolver(recoverySchema) });

  const onSubmit: SubmitHandler<RecoveryFormValues> = async ({ email }) => {
    setMessage(null);
    setHasError(false);

    try {
      await auth.sendPasswordResetEmail(email, `${window.location.origin}/reset-password`);
      setMessage(
        'Если аккаунт с такой почтой существует, мы отправили на неё ссылку для смены пароля.',
      );
    } catch {
      setHasError(true);
      setMessage('Не удалось отправить письмо. Проверьте адрес и попробуйте ещё раз.');
    }
  };

  return (
    <div className={styles.root}>
      <div className={styles.notice}>
        <span aria-hidden="true">i</span>
        <p>Ссылка действует 30 минут и может быть использована только один раз.</p>
      </div>

      <form className="auth-form" onSubmit={handleSubmit(onSubmit)} noValidate>
        <label htmlFor="recovery-email">Email аккаунта</label>
        <div className="auth-input">
          <input
            id="recovery-email"
            type="email"
            autoComplete="email"
            placeholder="name@company.ru"
            {...register('email')}
          />
          <span aria-hidden="true">@</span>
        </div>
        {errors.email && <p className="auth-field-error">{errors.email.message}</p>}

        {message && (
          <div
            className={`auth-message ${hasError ? 'auth-message-error' : styles.success}`}
            role={hasError ? 'alert' : 'status'}
          >
            {message}
          </div>
        )}

        <button className="auth-submit" type="submit" disabled={isSubmitting}>
          {isSubmitting ? 'Отправляем…' : 'Получить ссылку'} <span aria-hidden="true">→</span>
        </button>
      </form>

      <Link className={styles.backLink} to="/login">
        ← Вернуться ко входу
      </Link>
      <p className={styles.support}>
        Не пришло письмо? <a href="mailto:support@snaphost.ru">Напишите в поддержку</a>
      </p>
    </div>
  );
}
