import styles from './AdminTable.module.css';

import type { ReactNode } from 'react';

import { Skeleton } from '@/shared/ui/skeleton';

export interface AdminTableProps {
  headers: string[];
  isLoading?: boolean;
  isError?: boolean;
  isEmpty?: boolean;
  emptyText?: string;
  children: ReactNode;
  footer?: ReactNode;
}

/** The shared frame for every operator list: one card, one horizontally
 *  scrollable table, and the same loading/error/empty states so the screens do
 *  not each invent their own. */
function AdminTable({
  headers,
  isLoading,
  isError,
  isEmpty,
  emptyText = 'Ничего не найдено',
  children,
  footer,
}: AdminTableProps) {
  if (isError) {
    return (
      <p
        className={`${styles.root} text-sm text-red-600 bg-red-50 border border-red-200 rounded-lg px-4 py-3`}
      >
        Не удалось загрузить данные. Обновите страницу.
      </p>
    );
  }

  if (isLoading) {
    return (
      <div className="flex flex-col gap-3">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
      </div>
    );
  }

  return (
    <div className="bg-white border border-zinc-200 rounded-xl overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-zinc-200 text-left text-xs uppercase tracking-wide text-zinc-500">
              {headers.map((header) => (
                <th key={header} className="px-4 py-3 font-medium whitespace-nowrap">
                  {header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {isEmpty ? (
              <tr>
                <td colSpan={headers.length} className="px-4 py-10 text-center text-zinc-400">
                  {emptyText}
                </td>
              </tr>
            ) : (
              children
            )}
          </tbody>
        </table>
      </div>
      {footer}
    </div>
  );
}

export default AdminTable;
