import styles from './AuthLayout.module.css';

import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import { BrandLogo, SubpageHeader } from './SubpageChrome';

type AuthLayoutVariant = 'default' | 'signup' | 'recover';

export interface AuthLayoutProps {
  children: ReactNode;
  description: ReactNode;
  footerHref: string;
  footerLabel: string;
  kicker: string;
  panelCode: string;
  panelDescription: ReactNode;
  panelId: string;
  panelTitle: ReactNode;
  routeLabel: string;
  routePoints: readonly [string, string, string];
  step: string;
  title: ReactNode;
  variant?: AuthLayoutVariant;
}

export function AuthLayout({
  children,
  description,
  footerHref,
  footerLabel,
  kicker,
  panelCode,
  panelDescription,
  panelId,
  panelTitle,
  routeLabel,
  routePoints,
  step,
  title,
  variant = 'default',
}: AuthLayoutProps) {
  const isSignup = variant === 'signup';
  const isRecover = variant === 'recover';

  return (
    <main className={`${styles.root} auth-page${isSignup ? ' auth-page-signup' : ''}`}>
      <div className="auth-grid-bg" aria-hidden="true" />
      <SubpageHeader active="login" />
      <div className={`shell auth-layout${isSignup ? ' auth-layout-signup' : ''}`}>
        <section className="auth-story">
          <div>
            <span className="auth-kicker">
              <i /> {kicker}
            </span>
            <h1>{title}</h1>
            <p>{description}</p>
          </div>

          <div className="auth-route" aria-hidden="true">
            <span className="auth-route-label">{routeLabel}</span>
            <div className="auth-route-line">
              <i />
              <i />
              <i />
            </div>
            <div className="auth-route-points">
              {routePoints.map((point) => (
                <span key={point}>{point}</span>
              ))}
            </div>
          </div>
        </section>

        <section
          className={`auth-panel${isSignup ? ' auth-panel-tall auth-panel-signup' : ''}${isRecover ? ' auth-panel-recover' : ''}`}
          aria-labelledby={panelId}
        >
          <div className="auth-panel-top">
            <span>{panelCode}</span>
            <span className="auth-secure">● TLS secured</span>
          </div>
          <div className={`auth-form-wrap${isSignup ? ' auth-form-wrap-signup' : ''}`}>
            <div className="auth-mobile-brand">
              <BrandLogo />
            </div>
            <span className="auth-step">{step}</span>
            <h2 id={panelId}>{panelTitle}</h2>
            <p>{panelDescription}</p>
            {children}
          </div>
          <div className="auth-panel-bottom">
            <span>Защищено шифрованием</span>
            <Link to={footerHref}>{footerLabel} ↗</Link>
          </div>
        </section>
      </div>
    </main>
  );
}
