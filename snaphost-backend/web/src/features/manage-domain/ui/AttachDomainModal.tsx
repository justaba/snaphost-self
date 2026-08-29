import styles from './AttachDomainModal.module.css';

import { useEffect, useState } from 'react';

import { attachErrorMessage } from '@/entities/domain';
import type { ProjectGroup } from '@/entities/project';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Modal } from '@/shared/ui/modal';
import { useAttachDomain } from '../api/use-domains';

export interface AttachDomainModalProps {
  isOpen: boolean;
  onClose: () => void;
  projects: ProjectGroup[];
  /** Locks the target project — the project page already answers "which one". */
  fixedProjectId?: string;
  onAttached: (domain: string) => void;
}

/** Attaching needs two things: the hostname, and which project it publishes.
 *  Everything after that is DNS, which the page explains once the row exists. */
function AttachDomainModal({
  isOpen,
  onClose,
  projects,
  fixedProjectId,
  onAttached,
}: AttachDomainModalProps) {
  const attach = useAttachDomain();
  const [domain, setDomain] = useState('');
  const [projectId, setProjectId] = useState('');
  const [error, setError] = useState<string | null>(null);

  const fixedProject = fixedProjectId
    ? projects.find((p) => p.projectId === fixedProjectId)
    : undefined;

  useEffect(() => {
    if (!isOpen) return;
    setDomain('');
    setError(null);
    setProjectId(fixedProjectId ?? projects[0]?.projectId ?? '');
  }, [isOpen, projects, fixedProjectId]);

  const submit = async () => {
    setError(null);
    const trimmed = domain.trim().toLowerCase();
    if (!trimmed) {
      setError('Укажите домен.');
      return;
    }
    if (!projectId) {
      setError('Выберите проект, который будет открываться на этом домене.');
      return;
    }
    try {
      await attach.mutateAsync({ domain: trimmed, project_id: projectId });
      onAttached(trimmed);
      onClose();
    } catch (err) {
      setError(attachErrorMessage(err, 'Не удалось привязать домен. Попробуйте ещё раз.'));
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} title="Привязать домен" size="md">
      <div className={`${styles.root} px-6 py-5 flex flex-col gap-4`}>
        <Input
          label="Домен"
          placeholder="example.com"
          value={domain}
          autoComplete="off"
          spellCheck={false}
          maxLength={253}
          onChange={(e) => setDomain(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void submit();
          }}
        />

        {fixedProject ? (
          <p className="text-sm text-zinc-600">
            Домен будет привязан к проекту{' '}
            <span className="font-medium text-zinc-900">{fixedProject.label}</span> — не к
            отдельному деплою, поэтому он переживёт пересборку.
          </p>
        ) : (
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-zinc-700">Проект</span>
            <select
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              className="h-10 px-3 text-sm bg-white border border-zinc-300 rounded-lg text-zinc-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300"
            >
              {projects.length === 0 && <option value="">Нет доступных проектов</option>}
              {projects.map((project) => (
                <option key={project.projectId} value={project.projectId}>
                  {project.label}
                </option>
              ))}
            </select>
            <span className="text-xs text-zinc-500">
              Домен привязывается к проекту, а не к отдельному деплою, поэтому переживает
              пересборку.
            </span>
          </label>
        )}

        {error && (
          <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
            {error}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Отмена
          </Button>
          <Button loading={attach.isPending} disabled={projects.length === 0} onClick={submit}>
            Привязать
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export default AttachDomainModal;
