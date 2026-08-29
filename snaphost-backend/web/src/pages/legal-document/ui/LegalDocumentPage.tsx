import styles from './LegalDocumentPage.module.css';

import { useEffect } from 'react';
import { Link, Navigate, useParams } from 'react-router-dom';

import { getLegalDocument, legalDocuments } from '@/shared/config/legal';
import { getMarkdownHeadings } from '@/shared/lib/legal-markdown';
import { MarkdownDocument } from '@/shared/ui/markdown-document';
import { SubpageFooter, SubpageHeader } from '@/widgets/marketing-chrome';

function withoutDocumentHeader(source: string): string {
  return source
    .replace(/^#\s+.+\r?\n+/, '')
    .replace(/^Редакция от .+\r?\n+/, '')
    .trim();
}

export default function LegalDocumentPage() {
  const { documentSlug } = useParams();
  const document = getLegalDocument(documentSlug);
  const index = legalDocuments.findIndex((item) => item.slug === documentSlug);
  const headings = document ? getMarkdownHeadings(document.source) : [];
  const updated = document?.source.match(/^Редакция от (.+)$/m)?.[1] ?? '10 августа 2026 года';
  const displayTitle = document?.source.match(/^#\s+(.+)$/m)?.[1] ?? document?.title;

  useEffect(() => {
    if (document) window.document.title = `${document.title} — Snaphost`;
  }, [document]);

  if (!document) {
    return <Navigate to="/legal" replace />;
  }

  return (
    <main className={`${styles.root} legal-page`}>
      <div className="legal-hero">
        <SubpageHeader active="documents" />
        <div className="documents-grid-bg" aria-hidden="true" />
        <div className="shell legal-hero-inner">
          <Link className="legal-back" to="/legal">
            ← Все документы
          </Link>
          <span className="legal-number">DOC / {String(index + 1).padStart(2, '0')}</span>
          <h1>{displayTitle}</h1>
          <div className="legal-meta">
            <span>Редакция от {updated}</span>
            <span>{headings.length} разделов</span>
          </div>
        </div>
      </div>

      <div className="legal-paper">
        <div className="shell legal-layout">
          <aside className="legal-sidebar">
            <span className="legal-sidebar-label">В этом документе</span>
            <nav aria-label="Оглавление документа">
              {headings.map((heading) => (
                <a href={`#${heading.id}`} key={heading.id}>
                  {heading.title}
                </a>
              ))}
            </nav>
            <button className="legal-download" type="button" onClick={() => window.print()}>
              Версия для печати <span>↗</span>
            </button>
          </aside>

          <article className="legal-content" id="document-start">
            <header className="legal-print-header">
              <h1>{displayTitle}</h1>
              <p>Редакция от {updated}</p>
            </header>
            <p className="legal-lead">{document.description}</p>
            <MarkdownDocument source={withoutDocumentHeader(document.source)} />
            <div className="legal-contact">
              <span>Есть вопрос по документу?</span>
              <a href="mailto:support@snaphost.ru">support@snaphost.ru ↗</a>
            </div>
          </article>

          <aside className="legal-document-switcher">
            <span>Другие документы</span>
            {legalDocuments
              .filter((item) => item.slug !== document.slug)
              .slice(0, 4)
              .map((item) => (
                <Link to={`/legal/${item.slug}`} key={item.slug}>
                  {item.title}
                  <span>→</span>
                </Link>
              ))}
          </aside>
        </div>
      </div>
      <SubpageFooter />
    </main>
  );
}
