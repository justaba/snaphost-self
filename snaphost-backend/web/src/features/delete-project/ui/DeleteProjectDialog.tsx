import styles from './DeleteProjectDialog.module.css';

import { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';

import type { ProjectSummary } from '@/entities/project';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Modal } from '@/shared/ui/modal';
import { useToast } from '@/shared/ui/toast';
import { deleteProjectMessage, useDeleteProject } from '../api/use-delete-project';

export interface DeleteProjectDialogProps {
  project: ProjectSummary | null;
  isOpen: boolean;
  onClose: () => void;
}

/** Deleting a project takes its builds, its containers, its images and its
 *  domains with it, and none of that is restorable from the panel: the images
 *  are gone from the daemon and the rows are hard-deleted rather than marked.
 *
 *  So the confirmation is typed rather than a second button. The slug is
 *  visible on the same screen, which makes it a check against clicking the
 *  wrong row rather than a memory test. */
function DeleteProjectDialog({ project, isOpen, onClose }: DeleteProjectDialogProps) {
  const [confirmation, setConfirmation] = useState('');
  const deleteMut = useDeleteProject();
  const { toast } = useToast();

  useEffect(() => {
    if (isOpen) setConfirmation('');
  }, [isOpen, project?.id]);

  if (!project) return null;

  const confirmed = confirmation.trim() === project.slug;

  const handleDelete = () => {
    if (!confirmed) return;
    deleteMut.mutate(project.id, {
      onSuccess: () => {
        toast(`Проект ${project.slug} удалён`, 'success');
        onClose();
      },
      onError: (err) => toast(deleteProjectMessage(err), 'error'),
    });
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} size="md" title="Удалить проект">
      {/* Modal's body is a bare scroll container — its header carries px-6 py-4
          and the children are expected to supply their own. Without this the
          content sits flush against the dialog edge. */}
      <div className={`${styles.root} flex flex-col gap-4 px-6 py-5`}>
        <div className="flex gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3">
          <AlertTriangle size={18} className="mt-0.5 shrink-0 text-red-600" />
          <div className="text-sm text-red-800">
            <p className="font-medium">Это необратимо.</p>
            <p className="mt-1">
              Будут остановлены контейнеры и удалены Docker-образы всех сборок проекта, а затем
              удалены сами сборки ({project.deploys_count}) и привязанные домены (
              {project.domains_count}
              ). Восстановить из панели нельзя.
            </p>
            {project.running_count > 0 && (
              <p className="mt-1 font-medium">
                Сейчас работает сборок: {project.running_count}. Сайт станет недоступен.
              </p>
            )}
          </div>
        </div>

        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt className="text-zinc-500">Проект</dt>
          <dd className="text-zinc-900">{project.slug}</dd>
          <dt className="text-zinc-500">Источник</dt>
          <dd className="break-all font-mono text-xs text-zinc-600">{project.source_key}</dd>
        </dl>

        <div className="flex flex-col gap-1.5">
          <label htmlFor="delete-project-confirm" className="text-sm text-zinc-700">
            Введите <span className="font-mono text-zinc-900">{project.slug}</span> для
            подтверждения
          </label>
          <Input
            id="delete-project-confirm"
            value={confirmation}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setConfirmation(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleDelete();
            }}
          />
        </div>

        <p className="text-xs text-zinc-500">
          Если в проекте идёт сборка, удаление будет отклонено: она может создать контейнер уже
          после проверки.
        </p>

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={deleteMut.isPending}>
            Отмена
          </Button>
          <Button
            variant="danger"
            onClick={handleDelete}
            disabled={!confirmed}
            loading={deleteMut.isPending}
          >
            Удалить проект
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export default DeleteProjectDialog;
