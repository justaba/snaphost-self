import styles from './MarkdownDocument.module.css';

import { Fragment, type ReactNode } from 'react';

import { slugifyLegalHeading } from '@/shared/lib/legal-markdown';

interface MarkdownDocumentProps {
  source: string;
}

function renderInline(value: string, blockKey: string): ReactNode[] {
  const tokens = value.split(/(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\)|https?:\/\/[^\s]+)/g);

  return tokens.filter(Boolean).map((token, index) => {
    const key = `${blockKey}-inline-${index}`;

    if (token.startsWith('**') && token.endsWith('**')) {
      return <strong key={key}>{token.slice(2, -2)}</strong>;
    }

    if (token.startsWith('`') && token.endsWith('`')) {
      return (
        <code
          key={key}
          className={`${styles.root} rounded bg-slate-100 px-1.5 py-0.5 text-sm text-slate-800`}
        >
          {token.slice(1, -1)}
        </code>
      );
    }

    const markdownLink = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
    if (markdownLink) {
      const [, label, href] = markdownLink;
      const external = href.startsWith('http');
      return (
        <a
          key={key}
          href={href}
          className="font-medium text-violet-700 underline decoration-violet-300 underline-offset-2 hover:text-violet-900"
          {...(external ? { target: '_blank', rel: 'noreferrer' } : {})}
        >
          {label}
        </a>
      );
    }

    if (token.startsWith('http')) {
      const trailing = token.match(/[.,;:]$/)?.[0] ?? '';
      const href = trailing ? token.slice(0, -1) : token;
      return (
        <Fragment key={key}>
          <a
            href={href}
            target="_blank"
            rel="noreferrer"
            className="font-medium text-violet-700 underline decoration-violet-300 underline-offset-2 hover:text-violet-900"
          >
            {href}
          </a>
          {trailing}
        </Fragment>
      );
    }

    return <Fragment key={key}>{token}</Fragment>;
  });
}

export default function MarkdownDocument({ source }: MarkdownDocumentProps) {
  const lines = source.replace(/\r\n/g, '\n').trim().split('\n');
  const blocks: ReactNode[] = [];
  let paragraph: string[] = [];
  let listItems: string[] = [];
  let listType: 'ul' | 'ol' | null = null;
  let blockIndex = 0;

  const nextKey = () => `legal-block-${blockIndex++}`;

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    const key = nextKey();
    blocks.push(
      <p key={key} className="leading-7 text-slate-700">
        {renderInline(paragraph.join(' '), key)}
      </p>,
    );
    paragraph = [];
  };

  const flushList = () => {
    if (!listType || listItems.length === 0) return;
    const key = nextKey();
    const items = listItems.map((item, index) => (
      <li key={`${key}-${index}`}>{renderInline(item, `${key}-${index}`)}</li>
    ));
    blocks.push(
      listType === 'ul' ? (
        <ul key={key} className="list-disc space-y-2 pl-6 text-slate-700 marker:text-violet-500">
          {items}
        </ul>
      ) : (
        <ol
          key={key}
          className="list-decimal space-y-2 pl-6 text-slate-700 marker:font-semibold marker:text-violet-700"
        >
          {items}
        </ol>
      ),
    );
    listItems = [];
    listType = null;
  };

  lines.forEach((line) => {
    const trimmed = line.trim();
    const heading = trimmed.match(/^(#{1,3})\s+(.+)$/);
    const unorderedItem = trimmed.match(/^[-*]\s+(.+)$/);
    const orderedItem = trimmed.match(/^\d+\.\s+(.+)$/);

    if (!trimmed) {
      flushParagraph();
      flushList();
      return;
    }

    if (heading) {
      flushParagraph();
      flushList();
      const level = heading[1].length;
      const label = heading[2];
      const key = nextKey();
      const id = slugifyLegalHeading(label);
      if (level === 1) {
        blocks.push(
          <h1
            key={key}
            id={id}
            className="text-3xl font-black tracking-tight text-slate-950 sm:text-4xl"
          >
            {renderInline(label, key)}
          </h1>,
        );
      } else if (level === 2) {
        blocks.push(
          <h2 key={key} id={id} className="scroll-mt-24 pt-5 text-2xl font-bold text-slate-900">
            {renderInline(label, key)}
          </h2>,
        );
      } else {
        blocks.push(
          <h3 key={key} id={id} className="scroll-mt-24 pt-3 text-xl font-bold text-slate-900">
            {renderInline(label, key)}
          </h3>,
        );
      }
      return;
    }

    if (unorderedItem || orderedItem) {
      flushParagraph();
      const newListType = unorderedItem ? 'ul' : 'ol';
      if (listType && listType !== newListType) flushList();
      listType = newListType;
      listItems.push((unorderedItem ?? orderedItem)?.[1] ?? '');
      return;
    }

    if (trimmed === '---') {
      flushParagraph();
      flushList();
      blocks.push(<hr key={nextKey()} className="border-slate-200" />);
      return;
    }

    flushList();
    paragraph.push(trimmed);
  });

  flushParagraph();
  flushList();

  return <div className="legal-markdown">{blocks}</div>;
}
