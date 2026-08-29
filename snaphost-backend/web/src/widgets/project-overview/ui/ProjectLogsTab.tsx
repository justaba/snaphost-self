import { useEffect, useMemo, useRef, useState } from 'react';
import { Download, Search, Wifi, WifiOff } from 'lucide-react';
import { useDeployLogs, type LogLine, type LogStage } from '@/entities/deploy';
import { Input } from '@/shared/ui/input';
import styles from './ProjectLogsTab.module.css';

const ALL_STAGES: LogStage[] = [
  'pipeline',
  'clone',
  'detect',
  'validate',
  'build',
  'scan',
  'runtime-startup',
  'runtime',
  'runtime-shutdown',
];

const STAGE_LABEL: Record<LogStage, string> = {
  pipeline: 'pipeline',
  clone: 'clone',
  detect: 'detect',
  validate: 'validate',
  build: 'build',
  scan: 'scan',
  'runtime-startup': 'startup',
  runtime: 'runtime',
  'runtime-shutdown': 'shutdown',
};

export interface ProjectLogsTabProps {
  deployId: string;
}

function highlight(text: string, query: string): React.ReactNode {
  if (!query) return text;
  const safe = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(${safe})`, 'ig');
  const parts = text.split(re);
  return parts.map((part, i) =>
    re.test(part) ? (
      <mark key={i} className="bg-amber-300/40 text-amber-100 rounded px-0.5">
        {part}
      </mark>
    ) : (
      <span key={i}>{part}</span>
    ),
  );
}

function downloadLogs(filename: string, lines: LogLine[]) {
  const text = lines
    .map((l) => `${l.timestamp} [${l.stage}] ${l.level.toUpperCase()} ${l.text}`)
    .join('\n');
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function ProjectLogsTab({ deployId }: ProjectLogsTabProps) {
  const { logs, isConnected, error } = useDeployLogs(deployId);
  const [activeStages, setActiveStages] = useState<Set<LogStage>>(new Set(ALL_STAGES));
  const [query, setQuery] = useState('');
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return logs.filter((l) => {
      if (!activeStages.has(l.stage)) return false;
      if (q && !l.text.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [logs, activeStages, query]);

  useEffect(() => {
    if (!autoScroll) return;
    const node = containerRef.current;
    if (!node) return;
    node.scrollTop = node.scrollHeight;
  }, [filtered, autoScroll]);

  const handleScroll = () => {
    const node = containerRef.current;
    if (!node) return;
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
    setAutoScroll(distance < 32);
  };

  const toggleStage = (stage: LogStage) => {
    setActiveStages((prev) => {
      const next = new Set(prev);
      if (next.has(stage)) next.delete(stage);
      else next.add(stage);
      return next;
    });
  };

  return (
    <div className={styles.root}>
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2 text-xs">
          {isConnected ? (
            <span className="inline-flex items-center gap-1 text-emerald-700">
              <Wifi size={12} />
              Поток активен
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 text-zinc-500">
              <WifiOff size={12} />
              {error ? 'Polling fallback' : 'Нет соединения'}
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={() => downloadLogs(`deploy-${deployId}.log`, filtered)}
          className="inline-flex items-center gap-1.5 text-sm text-zinc-700 hover:text-zinc-900 px-3 py-1.5 border border-zinc-200 rounded-md hover:bg-zinc-50 transition-colors"
        >
          <Download size={14} />
          Скачать .log
        </button>
      </div>

      <Input
        placeholder="Поиск по логам…"
        iconLeft={<Search size={14} />}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />

      <div className="flex flex-wrap gap-1.5">
        {ALL_STAGES.map((stage) => {
          const active = activeStages.has(stage);
          return (
            <button
              key={stage}
              type="button"
              onClick={() => toggleStage(stage)}
              className={[
                'h-7 px-2.5 text-xs rounded-full border transition-colors duration-150 font-mono',
                active
                  ? 'bg-zinc-900 text-white border-zinc-900'
                  : 'bg-white text-zinc-600 border-zinc-200 hover:border-zinc-300',
              ].join(' ')}
            >
              {STAGE_LABEL[stage]}
            </button>
          );
        })}
      </div>

      <div
        ref={containerRef}
        onScroll={handleScroll}
        className="h-[420px] overflow-y-auto bg-zinc-950 text-zinc-100 font-mono text-xs rounded-lg p-3 border border-zinc-800"
      >
        {filtered.length === 0 ? (
          <div className="text-zinc-500">Логов пока нет</div>
        ) : (
          filtered.map((line, i) => (
            <div key={`${line.timestamp}-${i}`} className="whitespace-pre-wrap break-words">
              <span className="text-zinc-500">{new Date(line.timestamp).toLocaleTimeString()}</span>{' '}
              <span className="text-zinc-400">[{line.stage}]</span>{' '}
              <span
                className={
                  line.level === 'error'
                    ? 'text-red-400'
                    : line.level === 'warn'
                      ? 'text-amber-300'
                      : 'text-zinc-300'
                }
              >
                {highlight(line.text, query)}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

export default ProjectLogsTab;
