import styles from './DnsRecords.module.css';

import { CopyButton } from '@/shared/ui/copy-button';
import type { CustomDomain } from '../model/types';

interface RecordRowProps {
  type: string;
  name: string;
  value: string;
  hint?: string;
}

function RecordRow({ type, name, value, hint }: RecordRowProps) {
  return (
    <div className={`${styles.root} flex flex-col gap-1.5 py-3 first:pt-0 last:pb-0`}>
      <div className="flex items-center gap-2 text-xs uppercase tracking-wide text-zinc-500">
        <span className="font-medium">{type}</span>
        <span className="text-zinc-300">•</span>
        <span className="font-mono normal-case tracking-normal text-zinc-600 break-all">
          {name}
        </span>
      </div>
      <div className="flex items-start gap-2">
        <code className="flex-1 min-w-0 font-mono text-sm bg-zinc-100 border border-zinc-200 rounded-lg px-3 py-2 break-all select-all">
          {value}
        </code>
        <CopyButton value={value} ariaLabel={`Копировать значение записи ${type} ${name}`} />
      </div>
      {hint && <p className="text-xs text-zinc-500">{hint}</p>}
    </div>
  );
}

export interface DnsRecordsProps {
  domain: CustomDomain;
}

/** The records a user has to create, in the order they matter: ownership
 *  first (nothing routes without it), then where to send traffic. */
function DnsRecords({ domain }: DnsRecordsProps) {
  const { dns } = domain;
  const isApex = domain.domain.split('.').length === 2;

  return (
    <div className="divide-y divide-zinc-100">
      <RecordRow
        type={dns.verification_type}
        name={dns.verification_record}
        value={dns.verification_value}
        hint="Подтверждает, что домен ваш. Без неё домен не начнёт обслуживаться."
      />

      {dns.cname_target && (
        <RecordRow
          type="CNAME"
          name={domain.domain}
          value={dns.cname_target}
          hint={isApex ? dns.apex_note : undefined}
        />
      )}

      {dns.a_record_target && (
        <RecordRow
          type="A"
          name={domain.domain}
          value={dns.a_record_target}
          hint={
            isApex
              ? 'Для домена без поддомена используйте эту запись.'
              : 'Альтернатива CNAME, если провайдер его не поддерживает.'
          }
        />
      )}

      {!dns.cname_target && !dns.a_record_target && (
        <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-4 py-3 mt-3">
          Адрес, на который нужно направить домен, ещё не опубликован. Подтвердить владение можно
          уже сейчас — трафик пойдёт, как только адрес появится.
        </p>
      )}
    </div>
  );
}

export default DnsRecords;
