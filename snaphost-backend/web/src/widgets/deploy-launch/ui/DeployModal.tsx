import styles from './DeployModal.module.css';

import { useState } from 'react';

import type { CreateDeployResponse } from '@/entities/deploy';
import {
  DeployForm,
  extractDeployError,
  toCreateDeployRequest,
  useCreateDeploy,
  type DeployFormError,
  type DeployFormValues,
} from '@/features/create-deploy';
import { DeployProgress } from '@/features/monitor-deploy';
import { Modal } from '@/shared/ui/modal';

type Phase = 'idle' | 'submitting' | 'deploying' | 'error';

export interface DeployModalProps {
  isOpen: boolean;
  onClose: () => void;
}

function DeployModal({ isOpen, onClose }: DeployModalProps) {
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<DeployFormError | null>(null);
  const [created, setCreated] = useState<CreateDeployResponse | null>(null);
  const [formKey, setFormKey] = useState(0);
  const createDeploy = useCreateDeploy();

  const reset = () => {
    setPhase('idle');
    setError(null);
    setCreated(null);
    setFormKey((key) => key + 1);
    createDeploy.reset();
  };

  const handleClose = () => {
    if (phase === 'submitting') return;
    reset();
    onClose();
  };

  const handleSubmit = async (values: DeployFormValues) => {
    setPhase('submitting');
    setError(null);
    try {
      const result = await createDeploy.mutateAsync(toCreateDeployRequest(values));
      setCreated(result);
      setPhase('deploying');
    } catch (submitError) {
      setError(extractDeployError(submitError));
      setPhase('error');
    }
  };

  const showForm = phase !== 'deploying';

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
      title={phase === 'deploying' ? 'Деплой проекта' : 'Запустить проект'}
      size={phase === 'deploying' ? 'lg' : 'md'}
      closeOnBackdrop={phase !== 'submitting'}
    >
      <div className={styles.root}>
        {showForm && (
          <DeployForm
            key={formKey}
            submitting={phase === 'submitting'}
            error={error}
            onSubmit={handleSubmit}
            onCancel={handleClose}
          />
        )}

        {phase === 'deploying' && created && (
          <DeployProgress
            deployId={created.deploy_id}
            websocketUrl={created.websocket_url}
            onClose={handleClose}
          />
        )}
      </div>
    </Modal>
  );
}

export default DeployModal;
