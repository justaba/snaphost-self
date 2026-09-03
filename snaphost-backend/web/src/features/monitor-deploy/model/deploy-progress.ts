import type { DeployDetail } from '@/entities/deploy';

export type DeployStepKey = 'queued' | 'build' | 'provision' | 'start' | 'live';
export type DeployStepState = 'pending' | 'active' | 'done' | 'failed';

export interface DeployStep {
  key: DeployStepKey;
  label: string;
  state: DeployStepState;
}

export function deriveDeploySteps(deploy: DeployDetail | undefined): DeployStep[] {
  const status = deploy?.status;
  const saga = deploy?.saga;
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
    // Was 'Резерв', for the coin reservation removed in Task 1 item 3. The
    // step it replaces is real: 'pending' used to be traversed instantly on the
    // way to 'reserved' and is now where a saga waits before its first external
    // call.
    {
      key: 'queued',
      label: 'В очереди',
      // Done the moment the saga leaves 'pending', not when the build
      // succeeds. Keying it on image_built looks equivalent and is not: a build
      // that failed never sets it, so the stepper would mark the queue as the
      // failed stage and say the deploy never started building.
      state: stateOf(status === 'pending', Boolean(status) && status !== 'pending'),
    },
    { key: 'build', label: 'Сборка', state: stateOf(buildActive, buildDone) },
    // 'Сканирование' was here, keyed on buildDone with no active state of its
    // own — so it showed a tick for a step that never ran on its own account,
    // and after ADR 0003 removed the scanner it claimed a security control the
    // platform does not perform. There is no stage between the build and the
    // container now.
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
