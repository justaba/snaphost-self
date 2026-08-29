import styles from './LegalIndexPage.module.css';

import { useEffect } from 'react';
import { Link } from 'react-router-dom';

import { legalDocuments } from '@/shared/config/legal';
import { BrandLogo, SubpageFooter, SubpageHeader } from '@/widgets/marketing-chrome';

export default function LegalIndexPage() {
  useEffect(() => {
    document.title = 'Документы — Snaphost';
  }, []);

  return (
    <main className={`${styles.root} documents-index`}>
      <div className="documents-dark-head">
        <SubpageHeader active="documents" />
        <div className="documents-grid-bg" aria-hidden="true" />
        <div className="shell documents-index-hero">
          <span className="section-kicker">DOC / KNOWLEDGE BASE</span>
          <h1>
            Документы
            <br />
            <span>без мелкого шрифта.</span>
          </h1>
          <p>
            Условия работы платформы, правила обработки данных и информация для пользователей — в
            одном месте.
          </p>
        </div>
      </div>

      <section className="documents-list shell">
        <div className="documents-list-heading">
          <span>Все документы</span>
          <span>{String(legalDocuments.length).padStart(2, '0')} материалов</span>
        </div>
        <div className="document-cards">
          {legalDocuments.map((document, index) => (
            <Link className="document-card" to={`/legal/${document.slug}`} key={document.slug}>
              <span className="document-card-number">
                DOC / {String(index + 1).padStart(2, '0')}
              </span>
              <div>
                <h2>{document.title}</h2>
                <p>{document.description}</p>
              </div>
              <span className="document-card-arrow" aria-hidden="true">
                ↗
              </span>
            </Link>
          ))}
        </div>
        <div className="documents-help">
          <BrandLogo />
          <div>
            <h2>Не нашли ответ?</h2>
            <p>Напишите команде поддержки — поможем разобраться в условиях платформы.</p>
          </div>
          <a href="mailto:support@snaphost.ru">
            support@snaphost.ru <span>↗</span>
          </a>
        </div>
      </section>
      <SubpageFooter />
    </main>
  );
}
