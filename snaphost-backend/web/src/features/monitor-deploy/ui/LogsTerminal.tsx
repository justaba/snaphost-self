import styles from './LogsTerminal.module.css';

import { useEffect, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';

import type { LogLevel, LogLine } from '@/entities/deploy';

const LEVEL_CLASS: Record<LogLevel, string> = {
  info: styles.info,
  warn: styles.warn,
  error: styles.error,
};

interface LogsTerminalProps {
  logs: LogLine[];
  isConnected: boolean;
}

export default function LogsTerminal({ logs, isConnected }: LogsTerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);

  useEffect(() => {
    if (!autoScroll) return;
    const node = containerRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [logs, autoScroll]);

  const handleScroll = () => {
    const node = containerRef.current;
    if (!node) return;
    const distanceFromBottom = node.scrollHeight - node.scrollTop - node.clientHeight;
    setAutoScroll(distanceFromBottom < 32);
  };

  const resumeAutoScroll = () => {
    setAutoScroll(true);
    const node = containerRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  };

  return (
    <div className={styles.terminalWrap}>
      <div ref={containerRef} onScroll={handleScroll} className={styles.terminal}>
        {logs.length === 0 ? (
          <div className={styles.terminalEmpty}>
            {isConnected ? 'Ожидаем логи…' : 'Подключаемся к потоку логов…'}
          </div>
        ) : (
          logs.map((line, index) => (
            <div key={`${line.timestamp}-${index}`} className={styles.logLine}>
              <span className={styles.timestamp}>
                {new Date(line.timestamp).toLocaleTimeString()}
              </span>{' '}
              <span className={styles.stage}>[{line.stage}]</span>{' '}
              <span className={LEVEL_CLASS[line.level]}>{line.text}</span>
            </div>
          ))
        )}
      </div>

      {!autoScroll && (
        <button type="button" onClick={resumeAutoScroll} className={styles.newLogs}>
          <ChevronDown size={12} />
          Новые логи
        </button>
      )}
    </div>
  );
}
