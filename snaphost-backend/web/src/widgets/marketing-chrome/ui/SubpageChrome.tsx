import styles from './SubpageChrome.module.css';

import { Link } from 'react-router-dom';

import { useAuth } from '@/entities/session';

export function BrandLogo() {
  return (
    <Link className="brand" to="/" aria-label="Snaphost — на главную">
      <span className={`${styles.root} brand-mark`} aria-hidden="true">
        <span />
        <span />
      </span>
      <span>Snaphost</span>
    </Link>
  );
}

export function AccountButton() {
  const { isAuthenticated } = useAuth();

  return (
    <Link className="login-button" to={isAuthenticated ? '/dashboard' : '/login'}>
      {isAuthenticated ? 'Дашборд' : 'Войти'} <span className="arrow-icon">↗</span>
    </Link>
  );
}

interface SubpageHeaderProps {
  active?: 'documents' | 'login';
}

export function SubpageHeader({ active }: SubpageHeaderProps) {
  return (
    <header className="subpage-header shell">
      <BrandLogo />
      <nav className="subpage-nav" aria-label="Дополнительное меню">
        <a href="/#platform">Платформа</a>
        <a href="/#pricing">Тарифы</a>
        <Link className={active === 'documents' ? 'is-active' : ''} to="/legal">
          Документы
        </Link>
      </nav>
      {active === 'login' ? (
        <Link className="subpage-header-link" to="/">
          На главную <span>↗</span>
        </Link>
      ) : (
        <AccountButton />
      )}
    </header>
  );
}

export function SubpageFooter() {
  return (
    <footer className="subpage-footer">
      <div className="shell subpage-footer-inner">
        <BrandLogo />
        <span>© {new Date().getFullYear()} Snaphost</span>
        <a href="mailto:support@snaphost.ru">support@snaphost.ru</a>
      </div>
    </footer>
  );
}
