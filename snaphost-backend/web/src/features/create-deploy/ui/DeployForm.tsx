import { useState } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { ChevronDown } from 'lucide-react';
import { useForm, type SubmitHandler } from 'react-hook-form';

import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import EnvironmentFields from './EnvironmentFields';
import {
  deployFormSchema,
  type DeployFormError,
  type DeployFormValues,
} from '../model/deploy-form';
import styles from './DeployForm.module.css';

interface DeployFormProps {
  submitting: boolean;
  error: DeployFormError | null;
  onSubmit: (values: DeployFormValues) => Promise<void>;
  onCancel: () => void;
}

export default function DeployForm({ submitting, error, onSubmit, onCancel }: DeployFormProps) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const form = useForm<DeployFormValues>({
    resolver: zodResolver(deployFormSchema),
    defaultValues: { repo_url: '', branch: 'main', env: [] },
  });

  const submit: SubmitHandler<DeployFormValues> = async (values) => onSubmit(values);

  return (
    <form onSubmit={form.handleSubmit(submit)} className={styles.form} noValidate>
      <Input
        label="URL репозитория"
        placeholder="https://github.com/user/repo"
        disabled={submitting}
        error={form.formState.errors.repo_url?.message}
        {...form.register('repo_url')}
      />
      <Input
        label="Ветка"
        placeholder="main"
        disabled={submitting}
        error={form.formState.errors.branch?.message}
        {...form.register('branch')}
      />

      <div className={styles.advanced}>
        <button
          type="button"
          onClick={() => setShowAdvanced((value) => !value)}
          className={styles.toggle}
          aria-expanded={showAdvanced}
        >
          <ChevronDown
            size={14}
            className={[styles.chevron, showAdvanced ? styles.chevronOpen : ''].join(' ')}
          />
          Расширенные настройки
        </button>

        {showAdvanced && (
          <EnvironmentFields
            control={form.control}
            register={form.register}
            errors={form.formState.errors.env}
            disabled={submitting}
          />
        )}
      </div>

      {error && (
        <div className={styles.error} role="alert">
          <span>{error.message}</span>
        </div>
      )}

      <div className={styles.actions}>
        <Button type="button" variant="ghost" onClick={onCancel} disabled={submitting}>
          Отмена
        </Button>
        <Button type="submit" variant="primary" loading={submitting}>
          Деплой
        </Button>
      </div>
    </form>
  );
}
