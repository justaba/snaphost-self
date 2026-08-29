export function formatCompact(value: number | null | undefined): string {
  if (value == null) return '—';
  if (Math.abs(value) < 10_000) return value.toLocaleString('ru-RU');

  return new Intl.NumberFormat('ru-RU', {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value);
}
