export function slugifyLegalHeading(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-zа-яё0-9\s-]/gi, '')
    .trim()
    .replace(/\s+/g, '-');
}

export function getMarkdownHeadings(source: string): Array<{ id: string; title: string }> {
  return Array.from(source.matchAll(/^##\s+(.+)$/gm), (match) => ({
    id: slugifyLegalHeading(match[1]),
    title: match[1],
  }));
}
