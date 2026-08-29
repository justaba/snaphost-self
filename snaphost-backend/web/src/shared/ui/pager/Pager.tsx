import styles from './Pager.module.css';

import { ChevronLeft, ChevronRight } from 'lucide-react';

import { Button } from '@/shared/ui/button';

export interface PagerProps {
  total: number;
  limit: number;
  offset: number;
  onOffsetChange: (offset: number) => void;
  /** Shown while a page is in flight, so the counts do not look wrong. */
  isFetching?: boolean;
}

/** Offset paging over an exact total. The API caps `limit`, so a page is
 *  always bounded regardless of what is asked for here. */
function Pager({ total, limit, offset, onOffsetChange, isFetching }: PagerProps) {
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + limit, total);
  const canPrev = offset > 0;
  const canNext = offset + limit < total;

  return (
    <div
      className={`${styles.root} flex items-center justify-between gap-4 px-4 py-3 border-t border-zinc-200 text-sm text-zinc-500`}
    >
      <span>
        {from}–{to} из {total.toLocaleString('ru-RU')}
        {isFetching && ' · обновление…'}
      </span>
      <div className="flex items-center gap-2">
        <Button
          variant="secondary"
          size="sm"
          disabled={!canPrev}
          onClick={() => onOffsetChange(Math.max(0, offset - limit))}
          iconLeft={<ChevronLeft size={15} />}
          aria-label="Предыдущая страница"
        >
          Назад
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={!canNext}
          onClick={() => onOffsetChange(offset + limit)}
          iconRight={<ChevronRight size={15} />}
          aria-label="Следующая страница"
        >
          Вперёд
        </Button>
      </div>
    </div>
  );
}

export default Pager;
