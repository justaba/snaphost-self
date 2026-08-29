import type { DeployDetail } from '@/entities/deploy';

export type DeployStepKey = 'reserve' | 'build' | 'scan' | 'provision' | 'start' | 'live';
export type DeployStepState = 'pending' | 'active' | 'done' | 'failed';

export interface DeployStep {
  key: DeployStepKey;
  label: string;
  state: DeployStepState;
}

export function deriveDeploySteps(deploy: DeployDetail | undefined): DeployStep[] {
  const status = deploy?.status;
  const saga = deploy?.saga;
  const reserveDone = saga?.coins_reserved ?? false;
  const buildActive = status === 'building';
  const buildDone =
    (saga?.image_built ?? false) ||
    status === 'built' ||
    status === 'provisioning' ||
    status === 'running';
  const provisionActive = status === 'provisioning';
  const provisionDone = saga?.container_running ?? status === 'running';

  const stateOf = (active: boolean, done: boolean): DeployStepState => {
    if (done) return 'done';
    if (active) return 'active';
    return 'pending';
  };

  const steps: DeployStep[] = [
    {
      key: 'reserve',
      label: 'Резерв',
      state: stateOf(status === 'reserved' || status === 'pending', reserveDone),
    },
    { key: 'build', label: 'Сборка', state: stateOf(buildActive, buildDone) },
    { key: 'scan', label: 'Сканирование', state: stateOf(false, buildDone) },
    {
      key: 'provision',
      label: 'Подготовка',
      state: stateOf(provisionActive, provisionDone),
    },
    { key: 'start', label: 'Старт', state: stateOf(false, provisionDone) },
    { key: 'live', label: 'Работает', state: stateOf(false, status === 'running') },
  ];

  if (status === 'failed') {
    const failedStep = steps.find((step) => step.state !== 'done');
    if (failedStep) failedStep.state = 'failed';
  }

  return steps;
}
