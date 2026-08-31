import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ExternalLink, GitBranch, Layers } from 'lucide-react';
import { DeployStatusBadge as ProjectStatusBadge, useDeployDetail } from '@/entities/deploy';
import {
  startDeployMessage,
  stopDeployMessage,
  useStartDeploy,
  useStopDeploy,
} from '@/features/control-deploy';
import { useDeleteDeploy } from '@/features/delete-deploy';
import { Button } from '@/shared/ui/button';
import { Modal } from '@/shared/ui/modal';
import { Spinner } from '@/shared/ui/spinner';
import { Tabs } from '@/shared/ui/tabs';
import { useToast } from '@/shared/ui/toast';
import ProjectStatsTab from './ProjectStatsTab';
import ProjectResourcesTab from './ProjectResourcesTab';
import ProjectLogsTab from './ProjectLogsTab';
import styles from './ProjectDetailModal.module.css';

export interface ProjectDetailModalProps {
  deployId: string | null;
  isOpen: boolean;
  onClose: () => void;
}

type TabKey = 'stats' | 'resources' | 'logs';

function parseRepoName(repoUrl: string): string {
  try {
    const u = new URL(repoUrl);
    const segments = u.pathname
      .replace(/^\/+|\.git$/g, '')
      .split('/')
      .filter(Boolean);
    if (segments.length >= 2) return `${segments[0]}/${segments[1]}`;
    return segments.join('/') || repoUrl;
  } catch {
    return repoUrl;
  }
}

function ProjectDetailModal({ deployId, isOpen, onClose }: ProjectDetailModalProps) {
  const [tab, setTab] = useState<TabKey>('stats');
  const detailQuery = useDeployDetail(isOpen ? deployId : null);
  const deleteMut = useDeleteDeploy();
  const startMut = useStartDeploy();
  const stopMut = useStopDeploy();
  const { toast } = useToast();
  const deploy = detailQuery.data;

  const handleDelete = () => {
    if (!deploy) return;
    deleteMut.mutate(deploy.id, {
      onSuccess: () => {
        toast('Деплой удалён вместе с образом', 'success');
        onClose();
      },
      onError: (err) => toast(err.message || 'Не удалось удалить', 'error'),
    });
  };

  // Stopping keeps the image so the deploy can be started again; deleting is
  // what releases the disk. They used to be the same call.
  const handleStop = () => {
    if (!deploy) return;
    stopMut.mutate(deploy.id, {
      onSuccess: () => toast('Деплой остановлен, образ сохранён', 'success'),
      onError: (err) => toast(stopDeployMessage(err), 'error'),
    });
  };

  const handleStart = () => {
    if (!deploy) return;
    startMut.mutate(deploy.id, {
      onSuccess: () => toast('Деплой запущен', 'success'),
      onError: (err) => toast(startDeployMessage(err), 'error'),
    });
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} size="xl">
      <div className={styles.root}>
        <div className="px-6 pt-5 pb-4 border-b border-zinc-200">
          {deploy ? (
            <>
              <div className="flex items-start justify-between gap-4 flex-wrap">
                <div className="min-w-0">
                  <h2 className="text-lg font-medium text-zinc-900 truncate">
                    {parseRepoName(deploy.repo_url)}
                  </h2>
                  <div className="flex items-center gap-3 mt-1 text-xs text-zinc-500 flex-wrap">
                    <span className="inline-flex items-center gap-1 font-mono">
                      <GitBranch size={12} />
                      {deploy.branch}
                    </span>
                    {deploy.commit_sha && (
                      <span className="font-mono">{deploy.commit_sha.slice(0, 7)}</span>
                    )}
                    {deploy.endpoint_url && (
                      <a
                        href={deploy.endpoint_url}
                        target="_blank"
                        rel="noreferrer noopener"
                        className="inline-flex items-center gap-1 font-mono text-zinc-700 hover:text-zinc-900 truncate max-w-[16rem]"
                      >
                        <ExternalLink size={12} />
                        <span className="truncate">{deploy.endpoint_url}</span>
                      </a>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  {deploy.project_id && (
                    <Link
                      to={`/dashboard/projects/${deploy.project_id}`}
                      onClick={onClose}
                      className="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-sm font-medium text-zinc-700 bg-zinc-100 hover:bg-zinc-200 transition-colors"
                    >
                      <Layers size={15} />К проекту
                    </Link>
                  )}
                  <ProjectStatusBadge status={deploy.status} />
                </div>
              </div>
              <div className="mt-4">
                <Tabs
                  value={tab}
                  onChange={(v) => setTab(v as TabKey)}
                  ariaLabel="Разделы деплоя"
                  items={[
                    { value: 'stats', label: 'Статистика' },
                    { value: 'resources', label: 'Ресурсы' },
                    { value: 'logs', label: 'Логи' },
                  ]}
                />
              </div>
            </>
          ) : (
            <div className="flex items-center gap-2 text-sm text-zinc-500">
              <Spinner size="sm" /> Загружаем детали…
            </div>
          )}
        </div>

        <div className="flex-1 px-6 py-5">
          {detailQuery.isError && (
            <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800">
              Не удалось загрузить детали деплоя.
            </div>
          )}
          {deploy && (
            <>
              {tab === 'stats' && <ProjectStatsTab deploy={deploy} />}
              {tab === 'resources' && <ProjectResourcesTab deploy={deploy} />}
              {tab === 'logs' && <ProjectLogsTab deployId={deploy.id} />}
            </>
          )}
        </div>

        {deploy && (
          <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-zinc-200">
            {deploy.status === 'running' && (
              <Button variant="secondary" onClick={handleStop} loading={stopMut.isPending}>
                Остановить
              </Button>
            )}
            {/* Only a stopped deploy can start: it kept its image. A failed
                one never had a working one. */}
            {deploy.status === 'stopped' && (
              <Button variant="primary" onClick={handleStart} loading={startMut.isPending}>
                Запустить
              </Button>
            )}
            {deploy.status !== 'deleted' && (
              <Button variant="danger" onClick={handleDelete} loading={deleteMut.isPending}>
                Удалить
              </Button>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}

export default ProjectDetailModal;
