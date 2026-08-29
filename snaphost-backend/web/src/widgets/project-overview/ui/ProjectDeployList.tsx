import { useState } from 'react';
import { ArrowUpRight, Check, Globe } from 'lucide-react';

import { DeployStatusBadge as ProjectStatusBadge, type DeploySummary } from '@/entities/deploy';
import { attachErrorMessage, type CustomDomain } from '@/entities/domain';
import { useRepointDomain } from '@/features/manage-domain';
import { Button } from '@/shared/ui/button';
import { Modal } from '@/shared/ui/modal';
import { useToast } from '@/shared/ui/toast';
import styles from './ProjectDeployList.module.css';

export interface ProjectDeployListProps {
  deploys: DeploySummary[];
  /** Verified domains of this project — the only ones that can be repointed. */
  domains: CustomDomain[];
  onOpenDeploy: (deployId: string) => void;
}

function formatDate(value: string): string {
  return new Date(value).toLocaleString('ru-RU', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function shortRef(deploy: DeploySummary): string {
  return deploy.commit_sha ? deploy.commit_sha.slice(0, 7) : deploy.id.slice(0, 8);
}

/**
 * The project's build history. Every row is an immutable deploy that keeps its
 * own permanent URL even after a newer one supersedes it — which is exactly
 * what makes rollback a pointer move instead of a rebuild.
 */
function ProjectDeployList({ deploys, domains, onOpenDeploy }: ProjectDeployListProps) {
  const repoint = useRepointDomain();
  const { toast } = useToast();
  const [pickerDeploy, setPickerDeploy] = useState<DeploySummary | null>(null);
  const [pendingId, setPendingId] = useState<string | null>(null);

  const activate = async (deploy: DeploySummary, domain: CustomDomain) => {
    setPendingId(deploy.id);
    try {
      await repoint.mutateAsync({ id: domain.id, deployId: deploy.id });
      setPickerDeploy(null);
      toast(`${domain.domain} теперь открывает деплой ${shortRef(deploy)}.`, 'success');
    } catch (err) {
      toast(attachErrorMessage(err, 'Не удалось переключить домен.'), 'error');
    } finally {
      setPendingId(null);
    }
  };

  const onActivate = (deploy: DeploySummary) => {
    if (domains.length === 1) {
      void activate(deploy, domains[0]);
      return;
    }
    // More than one domain: which hostname moves is the user's decision, not ours.
    setPickerDeploy(deploy);
  };

  /** Domains currently served by this deploy. */
  const servedBy = (deploy: DeploySummary): CustomDomain[] =>
    domains.filter((d) => d.target_deploy_id === deploy.id);

  return (
    <div className={`${styles.root} divide-y divide-zinc-100`}>
      {deploys.map((deploy) => {
        const active = servedBy(deploy);
        const canActivate =
          deploy.status === 'running' && domains.length > 0 && active.length === 0;

        return (
          <div
            key={deploy.id}
            className="flex items-start justify-between gap-4 px-5 py-4 flex-wrap"
          >
            <div className="min-w-0 flex flex-col gap-1.5">
              <div className="flex items-center gap-2 flex-wrap">
                <button
                  type="button"
                  onClick={() => onOpenDeploy(deploy.id)}
                  className="font-mono text-sm text-zinc-900 hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300 rounded"
                >
                  {shortRef(deploy)}
                </button>
                <ProjectStatusBadge status={deploy.status} size="sm" />
                {active.map((domain) => (
                  <span
                    key={domain.id}
                    className="inline-flex items-center gap-1 text-xs font-medium px-2 py-0.5 rounded-full border bg-emerald-50 text-emerald-700 border-emerald-200"
                  >
                    <Globe size={12} />
                    {domain.domain}
                  </span>
                ))}
              </div>

              <div className="text-xs text-zinc-500">
                {formatDate(deploy.created_at)}
                {deploy.branch && (
                  <>
                    {' · '}
                    <span className="font-mono">{deploy.branch}</span>
                  </>
                )}
              </div>

              {deploy.endpoint_url && (
                <a
                  href={deploy.endpoint_url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1 text-xs text-zinc-500 hover:text-zinc-900 truncate"
                >
                  {deploy.endpoint_url}
                  <ArrowUpRight size={12} className="shrink-0" />
                </a>
              )}
            </div>

            <div className="flex items-center gap-2">
              {active.length > 0 && (
                <span className="inline-flex items-center gap-1 text-xs text-emerald-700">
                  <Check size={13} /> активен
                </span>
              )}
              {canActivate && (
                <Button
                  variant="secondary"
                  size="sm"
                  loading={pendingId === deploy.id}
                  onClick={() => onActivate(deploy)}
                >
                  Сделать активным
                </Button>
              )}
              <Button variant="ghost" size="sm" onClick={() => onOpenDeploy(deploy.id)}>
                Подробнее
              </Button>
            </div>
          </div>
        );
      })}

      <Modal
        isOpen={pickerDeploy !== null}
        onClose={() => setPickerDeploy(null)}
        title="Какой домен переключить?"
        size="sm"
      >
        <div className="px-6 py-5 flex flex-col gap-3">
          <p className="text-sm text-zinc-600">
            У проекта несколько доменов. Выберите тот, который должен открывать деплой{' '}
            <span className="font-mono">{pickerDeploy ? shortRef(pickerDeploy) : ''}</span>.
          </p>
          {domains.map((domain) => (
            <Button
              key={domain.id}
              variant="secondary"
              loading={pendingId === pickerDeploy?.id}
              onClick={() => pickerDeploy && activate(pickerDeploy, domain)}
            >
              {domain.domain}
            </Button>
          ))}
          <div className="flex justify-end">
            <Button variant="ghost" onClick={() => setPickerDeploy(null)}>
              Отмена
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

export default ProjectDeployList;
