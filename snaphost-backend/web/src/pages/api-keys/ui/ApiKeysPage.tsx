import styles from './ApiKeysPage.module.css';

import { useState } from 'react';
import { Check, Copy, KeyRound, Plus, Trash2 } from 'lucide-react';

import type { ApiKeyInfo } from '@/entities/api-key';
import { useApiKeys, useCreateApiKey, useRevokeApiKey } from '@/features/manage-api-keys';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/empty-state';
import { Input } from '@/shared/ui/input';
import { Modal } from '@/shared/ui/modal';
import { Skeleton } from '@/shared/ui/skeleton';
import { useToast } from '@/shared/ui/toast';

function formatDate(value: string | null | undefined): string {
  if (!value) return '—';
  return new Date(value).toLocaleString('ru-RU', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

interface CreatedKeyModalProps {
  plaintext: string;
  onClose: () => void;
}

/** Shows the freshly minted sk_ key exactly once, with a copy button. */
function CreatedKeyModal({ plaintext, onClose }: CreatedKeyModalProps) {
  const [copied, setCopied] = useState(false);
  const { toast } = useToast();

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(plaintext);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toast('Не удалось скопировать — выделите ключ вручную.', 'error');
    }
  };

  return (
    <Modal isOpen onClose={onClose} title="Ключ создан" size="lg" closeOnBackdrop={false}>
      <div className={`${styles.root} px-6 py-5 flex flex-col gap-4`}>
        <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-4 py-3">
          Скопируйте ключ сейчас — он показывается только один раз. Восстановить его нельзя, только
          создать новый.
        </p>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 font-mono text-sm bg-zinc-100 border border-zinc-200 rounded-lg px-3 py-2.5 break-all select-all">
            {plaintext}
          </code>
          <Button
            variant="secondary"
            size="sm"
            onClick={copy}
            iconLeft={copied ? <Check size={16} /> : <Copy size={16} />}
          >
            {copied ? 'Скопировано' : 'Копировать'}
          </Button>
        </div>
        <div className="text-sm text-zinc-500">
          Использование: заголовок{' '}
          <code className="font-mono text-xs bg-zinc-100 rounded px-1.5 py-0.5">
            Authorization: Bearer sk_…
          </code>{' '}
          для API, либо переменная{' '}
          <code className="font-mono text-xs bg-zinc-100 rounded px-1.5 py-0.5">
            SNAPHOST_API_KEY
          </code>{' '}
          для MCP-сервера в вашем редакторе.
        </div>
        <div className="flex justify-end">
          <Button onClick={onClose}>Готово</Button>
        </div>
      </div>
    </Modal>
  );
}

function ApiKeysPage() {
  const { data, isLoading, isError } = useApiKeys();
  const createKey = useCreateApiKey();
  const revokeKey = useRevokeApiKey();
  const { toast } = useToast();

  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<ApiKeyInfo | null>(null);

  const onCreate = async () => {
    try {
      const result = await createKey.mutateAsync({ name: newName.trim() || undefined });
      setCreateOpen(false);
      setNewName('');
      setCreatedKey(result.key);
    } catch {
      toast('Не удалось создать ключ. Попробуйте ещё раз.', 'error');
    }
  };

  const onRevoke = async () => {
    if (!revokeTarget) return;
    try {
      await revokeKey.mutateAsync(revokeTarget.id);
      setRevokeTarget(null);
      toast('Ключ отозван.', 'success');
    } catch {
      toast('Не удалось отозвать ключ.', 'error');
    }
  };

  const keys = data?.keys ?? [];

  return (
    <div className="max-w-7xl mx-auto">
      <header className="mb-6 flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">API-ключи</h1>
          <p className="text-sm text-zinc-500 mt-1">
            Доступ для MCP-сервера, редактора и CLI без входа через браузер
          </p>
        </div>
        <Button iconLeft={<Plus size={16} />} onClick={() => setCreateOpen(true)}>
          Создать ключ
        </Button>
      </header>

      {isLoading && (
        <div className="flex flex-col gap-3">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      )}

      {isError && (
        <p className="text-sm text-red-600 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
          Не удалось загрузить ключи. Обновите страницу.
        </p>
      )}

      {!isLoading && !isError && keys.length === 0 && (
        <EmptyState
          icon={<KeyRound className="w-16 h-16" strokeWidth={1.5} />}
          title="Пока нет ни одного ключа"
          description="Создайте ключ, чтобы деплоить из редактора через MCP-сервер или API."
        />
      )}

      {keys.length > 0 && (
        <div className="bg-white border border-zinc-200 rounded-xl overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-zinc-200 text-left text-xs uppercase tracking-wide text-zinc-500">
                  <th className="px-4 py-3 font-medium">Название</th>
                  <th className="px-4 py-3 font-medium">Ключ</th>
                  <th className="px-4 py-3 font-medium">Создан</th>
                  <th className="px-4 py-3 font-medium">Использован</th>
                  <th className="px-4 py-3" />
                </tr>
              </thead>
              <tbody>
                {keys.map((key) => (
                  <tr key={key.id} className="border-b border-zinc-100 last:border-b-0">
                    <td className="px-4 py-3 text-zinc-900">{key.name || 'Без названия'}</td>
                    <td className="px-4 py-3">
                      <code className="font-mono text-xs text-zinc-600">{key.prefix}…</code>
                    </td>
                    <td className="px-4 py-3 text-zinc-500">{formatDate(key.created_at)}</td>
                    <td className="px-4 py-3 text-zinc-500">{formatDate(key.last_used_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        iconLeft={<Trash2 size={15} />}
                        onClick={() => setRevokeTarget(key)}
                        aria-label={`Отозвать ключ ${key.name || key.prefix}`}
                      >
                        Отозвать
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <Modal
        isOpen={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Новый API-ключ"
        size="md"
      >
        <div className="px-6 py-5 flex flex-col gap-4">
          <Input
            label="Название (необязательно)"
            placeholder="Например: мой ноутбук / Cursor"
            value={newName}
            maxLength={100}
            onChange={(e) => setNewName(e.target.value)}
          />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setCreateOpen(false)}>
              Отмена
            </Button>
            <Button loading={createKey.isPending} onClick={onCreate}>
              Создать
            </Button>
          </div>
        </div>
      </Modal>

      {createdKey && <CreatedKeyModal plaintext={createdKey} onClose={() => setCreatedKey(null)} />}

      <Modal
        isOpen={revokeTarget !== null}
        onClose={() => setRevokeTarget(null)}
        title="Отозвать ключ?"
        size="sm"
      >
        <div className="px-6 py-5 flex flex-col gap-4">
          <p className="text-sm text-zinc-600">
            Ключ «{revokeTarget?.name || revokeTarget?.prefix}» перестанет работать сразу. Всё, что
            им пользуется (MCP-сервер, скрипты), потеряет доступ.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setRevokeTarget(null)}>
              Отмена
            </Button>
            <Button variant="danger" loading={revokeKey.isPending} onClick={onRevoke}>
              Отозвать
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

export default ApiKeysPage;
