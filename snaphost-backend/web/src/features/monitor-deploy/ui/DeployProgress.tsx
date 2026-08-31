import { useEffect, useMemo, useRef } from 'react';
import { ExternalLink } from 'lucide-react';

import { useDeployDetail, useDeployLogs, type DeployDetail } from '@/entities/deploy';
import { Button } from '@/shared/ui/button';
import { Spinner } from '@/shared/ui/spinner';
import { deriveDeploySteps } from '../model/deploy-progress';
import DeployStepper from './DeployStepper';
import LogsTerminal from './LogsTerminal';
import styles from './DeployProgress.module.css';

export interface DeployProgressProps {
  deployId: string;
  websocketUrl?: string | null;
  onClose: () => void;
  onSuccess?: (deploy: DeployDetail) => void;
}

function DeployProgress({ deployId, websocketUrl, onClose, onSuccess }: DeployProgressProps) {
  const detailQuery = useDeployDetail(deployId);
  const logs = useDeployLogs(deployId, websocketUrl);
  const deploy = detailQuery.data;
  const steps = useMemo(() => deriveDeploySteps(deploy), [deploy]);

  const successFiredRef = useRef(false);
  useEffect(() => {
    if (deploy?.status === 'running' && !successFiredRef.current) {
      successFiredRef.current = true;
      onSuccess?.(deploy);
    }
  }, [deploy, onSuccess]);

  return (
    <div className={styles.container}>
      <DeployStepper steps={steps} />
      <LogsTerminal logs={logs.logs} isConnected={logs.isConnected} />

      {deploy?.status === 'running' && deploy.endpoint_url && (
        <div className={styles.success}>
          <div className={styles.successInfo}>
            <span className={styles.successTitle}>Деплой готов</span>
            <a
              href={deploy.endpoint_url}
              target="_blank"
              rel="noreferrer noopener"
              className={styles.url}
            >
              {deploy.endpoint_url}
            </a>
          </div>
          <Button
            variant="primary"
            size="md"
            iconRight={<ExternalLink size={14} />}
            onClick={() => window.open(deploy.endpoint_url ?? '', '_blank', 'noopener')}
          >
            Открыть в новом окне
          </Button>
        </div>
      )}

      {deploy?.status === 'failed' && (
        <div className={styles.failure}>
          <div>
            <div className={styles.failureTitle}>Деплой завершился с ошибкой</div>
            <div className={styles.failureReason}>
              {deploy.failure_reason ?? 'Неизвестная ошибка'}
            </div>
          </div>
          {/* There is no restart here. A failed build produced no working
              image — the sweep reclaims those — so the only way forward is a
              new deploy of the project, which is a different button on a
              different screen. The one that used to be here called an endpoint
              that has never existed in this backend. */}
          <div className={styles.failureActions}>
            <Button variant="ghost" size="sm" onClick={onClose}>
              Закрыть
            </Button>
          </div>
        </div>
      )}

      {!deploy && detailQuery.isLoading && (
        <div className={styles.loading}>
          <Spinner /> <span className={styles.loadingText}>Загружаем статус…</span>
        </div>
      )}
    </div>
  );
}

export default DeployProgress;
