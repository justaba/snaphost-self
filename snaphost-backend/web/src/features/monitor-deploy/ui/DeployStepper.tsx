import styles from './DeployStepper.module.css';

import {
  Check,
  Clock,
  Hammer,
  Loader2,
  Play,
  Server,
  ShieldCheck,
  Sparkles,
  X,
} from 'lucide-react';
import type { ReactNode } from 'react';

import type { DeployStep, DeployStepKey } from '../model/deploy-progress';

const STEP_ICONS: Record<DeployStepKey, ReactNode> = {
  queued: <Clock size={16} />,
  build: <Hammer size={16} />,
  scan: <ShieldCheck size={16} />,
  provision: <Server size={16} />,
  start: <Play size={16} />,
  live: <Sparkles size={16} />,
};

function StepBadge({ step }: { step: DeployStep }) {
  const content =
    step.state === 'done' ? (
      <Check size={16} />
    ) : step.state === 'failed' ? (
      <X size={16} />
    ) : step.state === 'active' ? (
      <Loader2 size={16} />
    ) : (
      STEP_ICONS[step.key]
    );

  return (
    <div className={styles.step}>
      <div className={[styles.badge, styles[step.state]].join(' ')}>{content}</div>
      <span
        className={[styles.stepLabel, step.state === 'pending' ? styles.pendingLabel : ''].join(
          ' ',
        )}
      >
        {step.label}
      </span>
    </div>
  );
}

export default function DeployStepper({ steps }: { steps: DeployStep[] }) {
  return (
    <div className={styles.stepper} aria-label="Этапы деплоя">
      {steps.map((step, index) => (
        <div key={step.key} className={styles.stepGroup}>
          <StepBadge step={step} />
          {index < steps.length - 1 && (
            <span
              aria-hidden="true"
              className={[styles.connector, step.state === 'done' ? styles.connectorDone : ''].join(
                ' ',
              )}
            />
          )}
        </div>
      ))}
    </div>
  );
}
