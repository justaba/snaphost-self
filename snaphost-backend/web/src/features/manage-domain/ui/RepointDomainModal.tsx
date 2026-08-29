import styles from './RepointDomainModal.module.css';

import { useState } from 'react';
import { ArrowUpRight, Check } from 'lucide-react';

import type { DeploySummary } from '@/entities/deploy';
import { attachErrorMessage, type CustomDomain } from '@/entities/domain';
import type { ProjectGroup } from '@/entities/project';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/empty-state';
import { Modal } from '@/shared/ui/modal';
import { useRepointDomain } from '../api/use-domains';

export interface RepointDomainModalProps {
  domain: CustomDomain | null;
  project: ProjectGroup | undefined;
  onClose: () => void;
  onDone: () => void;
}

function formatDate(value: string): string {
  return new Date(value).toLocaleString('ru-RU', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/**
 * Every deploy of the project, each still reachable at its own permanent URL.
 * Choosing one moves the domain's pointer — that is both publishing a new
 * build and rolling back to an older one. Nothing is rebuilt either way.
 */
function RepointDomainModal({ domain, project, onClose, onDone }: RepointDomainModalProps) {
  const repoint = useRepointDomain();
  const [error, setError] = useState<string | null>(null);
  const [pendingId, setPendingId] = useState<string | null>(null);

  const candidates: DeploySummary[] = (project?.deploys ?? []).filter(
    (d) => d.status === 'running',
  );

  const choose = async (deploy: DeploySummary) => {
    if (!domain) return;
    setError(null);
    setPendingId(deploy.id);
    try {
      await repoint.mutateAsync({ id: domain.id, deployId: deploy.id });
      onDone();
      onClose();
    } catch (err) {
      setError(attachErrorMessage(err, 'Не удалось переключить домен. Попробуйте ещё раз.'));
    } finally {
      setPendingId(null);
    }
  };

  return (
    <Modal
      isOpen={domain !== null}
      onClose={onClose}
      title={domain ? `Куда указывает ${domain.domain}` : 'Выбор деплоя'}
      size="lg"
    >
      <div className={`${styles.root} px-6 py-5 flex flex-col gap-4`}>
        <p className="text-sm text-zinc-500">
          Домен указывает на один деплой. Переключение мгновенное и без пересборки: предыдущий
          деплой продолжает отвечать до самого момента переключения, а его собственный адрес
          остаётся рабочим.
        </p>

        {candidates.length === 0 && (
          <EmptyState
            title="Нет запущенных деплоев"
            description="Домен может указывать только на запущенный деплой этого проекта. Запустите новый — и вернитесь сюда."
          />
        )}

        {candidates.length > 0 && (
          <ul className="flex flex-col gap-2">
            {candidates.map((deploy) => {
              const isCurrent = deploy.id === domain?.target_deploy_id;
              return (
                <li
                  key={deploy.id}
                  className={[
                    'flex items-center justify-between gap-4 rounded-lg border px-4 py-3',
                    isCurrent ? 'border-emerald-200 bg-emerald-50' : 'border-zinc-200 bg-white',
                  ].join(' ')}
                >
                  <div className="min-w-0 flex flex-col gap-1">
                    <div className="flex items-center gap-2 text-sm text-zinc-900">
                      <span className="font-mono text-xs text-zinc-500">
                        {deploy.commit_sha ? deploy.commit_sha.slice(0, 7) : deploy.id.slice(0, 8)}
                      </span>
                      <span className="text-zinc-400">•</span>
                      <span>{formatDate(deploy.created_at)}</span>
                      {isCurrent && (
                        <span className="inline-flex items-center gap-1 text-xs font-medium text-emerald-700">
                          <Check size={13} /> сейчас активен
                        </span>
                      )}
                    </div>
                    {deploy.endpoint_url && (
                      <a
                        href={deploy.endpoint_url}
                        target="_blank"
                        rel="noreferrer"
                        className="inline-flex items-center gap-1 text-xs text-zinc-500 hover:text-zinc-900 truncate"
                      >
                        {deploy.endpoint_url}
                        <ArrowUpRight size={12} className="shrink-0" />
                      </a>
                    )}
                  </div>
                  <Button
                    variant={isCurrent ? 'ghost' : 'secondary'}
                    size="sm"
                    disabled={isCurrent}
                    loading={pendingId === deploy.id}
                    onClick={() => choose(deploy)}
                  >
                    {isCurrent ? 'Активен' : 'Переключить'}
                  </Button>
                </li>
              );
            })}
          </ul>
        )}

        {error && (
          <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
            {error}
          </p>
        )}

        <div className="flex justify-end">
          <Button variant="secondary" onClick={onClose}>
            Закрыть
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export default RepointDomainModal;
