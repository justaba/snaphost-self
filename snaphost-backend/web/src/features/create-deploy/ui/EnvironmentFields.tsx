import styles from './EnvironmentFields.module.css';

import { Plus, Trash2 } from 'lucide-react';
import {
  useFieldArray,
  type Control,
  type FieldErrors,
  type UseFormRegister,
} from 'react-hook-form';

import type { DeployFormValues } from '../model/deploy-form';

interface EnvironmentFieldsProps {
  control: Control<DeployFormValues>;
  register: UseFormRegister<DeployFormValues>;
  errors: FieldErrors<DeployFormValues>['env'];
  disabled: boolean;
}

export default function EnvironmentFields({
  control,
  register,
  errors,
  disabled,
}: EnvironmentFieldsProps) {
  const { fields, append, remove } = useFieldArray({ control, name: 'env' });

  return (
    <div className={styles.environment}>
      <p className={styles.hint}>
        Переменные окружения. Ключи в формате <code>A-Z_</code>, префикс <code>SNAPHOST_</code>{' '}
        зарезервирован.
      </p>

      {fields.map((field, index) => (
        <div key={field.id} className={styles.environmentRow}>
          <label className={styles.field}>
            <span className="sr-only">Имя переменной {index + 1}</span>
            <input
              className={styles.input}
              placeholder="KEY"
              disabled={disabled}
              {...register(`env.${index}.key`)}
            />
            {errors?.[index]?.key?.message && (
              <span className={styles.fieldError}>{errors[index].key.message}</span>
            )}
          </label>

          <label className={styles.field}>
            <span className="sr-only">Значение переменной {index + 1}</span>
            <input
              className={styles.input}
              placeholder="value"
              disabled={disabled}
              {...register(`env.${index}.value`)}
            />
          </label>

          <button
            type="button"
            onClick={() => remove(index)}
            aria-label={`Удалить переменную ${index + 1}`}
            className={styles.removeButton}
          >
            <Trash2 size={14} />
          </button>
        </div>
      ))}

      <button
        type="button"
        onClick={() => append({ key: '', value: '' })}
        className={styles.addButton}
      >
        <Plus size={14} />
        Добавить переменную
      </button>
    </div>
  );
}
