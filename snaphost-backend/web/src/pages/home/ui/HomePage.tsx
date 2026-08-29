import styles from './HomePage.module.css';

import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';

import { AccountButton, BrandLogo } from '@/widgets/marketing-chrome';

const plans = [
  {
    name: 'Free',
    label: 'Для старта',
    description: 'Попробуйте Snaphost и запустите первый проект без карты.',
    features: [
      '1 активный проект',
      '5 успешных деплоев в месяц',
      '1 ГБ исходящего трафика',
      '10 000 HTTP-запросов',
      'Результат сборки до 100 МБ',
      'Собственный домен',
      'Автоматический SSL',
      'Хранение 3 последних деплоев',
      'Preview-деплои на 24 часа',
    ],
    featured: false,
  },
  {
    name: 'Plus',
    label: 'Для пет-проектов',
    description: 'Больше проектов, Git-автоматизация и быстрый откат релизов.',
    features: [
      '3 активных проекта',
      '30 успешных деплоев в месяц',
      '10 ГБ исходящего трафика',
      '100 000 HTTP-запросов',
      'Результат сборки до 250 МБ',
      '3 собственных домена',
      'Технические домены и автоматический SSL',
      'Автодеплой при push в Git',
      'Хранение 10 последних деплоев',
      'Rollback в один клик',
      'Preview-деплои на 7 дней',
      'Логи сборки за 7 дней',
    ],
    featured: true,
  },
  {
    name: 'Pro',
    label: 'Для продакшена',
    description: 'Ресурсы, аналитика и приоритет для активно растущих продуктов.',
    features: [
      '10 активных проектов',
      '100 успешных деплоев в месяц',
      '50 ГБ исходящего трафика',
      '500 000 HTTP-запросов',
      'Результат сборки до 500 МБ',
      'Сборка до 15 минут',
      '2 параллельные сборки',
      'До 20 собственных доменов',
      'Автоматический SSL',
      'Preview для веток и pull request',
      'Хранение 30 последних деплоев',
      'Preview-деплои на 30 дней',
      'Защита Preview паролем',
      'Rollback в один клик',
      'Логи за 30 дней',
      'Базовая аналитика трафика и ошибок',
      'Приоритет в очереди сборки',
    ],
    featured: false,
  },
] as const;

const stacks = [
  { name: 'React', mark: 'R', color: '#67d9f7' },
  { name: 'Vue', mark: 'V', color: '#54e3a4' },
  { name: 'Angular', mark: 'A', color: '#ff617c' },
  { name: 'Svelte', mark: 'S', color: '#ff7657' },
  { name: 'Next.js', mark: 'N', color: '#ffffff' },
  { name: 'Node.js', mark: 'JS', color: '#8cdd77' },
] as const;

type BuildState = 'idle' | 'building' | 'ready' | 'error';

export default function Home() {
  const [repo, setRepo] = useState('');
  const [buildState, setBuildState] = useState<BuildState>('idle');

  useEffect(() => {
    document.title = 'Snaphost — мгновенный деплой веб-приложений';
  }, []);

  useEffect(() => {
    if (buildState !== 'building') return;
    const timer = window.setTimeout(() => setBuildState('ready'), 1900);
    return () => window.clearTimeout(timer);
  }, [buildState]);

  const startBuild = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const normalized = repo.trim();
    if (!/^https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/?$/i.test(normalized)) {
      setBuildState('error');
      return;
    }
    setBuildState('building');
  };

  return (
    <main id="top" className={`${styles.root} marketing-home`}>
      <section className="hero">
        <div className="hero-grid" aria-hidden="true" />
        <div className="signal signal-one" aria-hidden="true" />
        <div className="signal signal-two" aria-hidden="true" />
        <div className="signal signal-three" aria-hidden="true" />

        <header className="navbar shell">
          <BrandLogo />
          <nav className="nav-links" aria-label="Главное меню">
            <a href="#platform">Платформа</a>
            <a href="#workflow">Как это работает</a>
            <a href="#pricing">Тарифы</a>
            <Link to="/legal">Документы</Link>
          </nav>
          <AccountButton />
        </header>

        <div className="hero-content shell">
          <div className="hero-copy">
            <div className="eyebrow">
              <span className="live-dot" /> AI-powered cloud deployment
            </div>
            <h1>
              Из репозитория
              <br />в <span>продакшен.</span>
            </h1>
            <p>
              Snaphost анализирует код, настраивает инфраструктуру и публикует веб-приложение — пока
              вы успеваете сделать один глоток кофе.
            </p>
          </div>

          <form className="deploy-form" onSubmit={startBuild} noValidate>
            <div className={`deploy-field ${buildState === 'error' ? 'has-error' : ''}`}>
              <span className="repo-icon" aria-hidden="true">
                ⌘
              </span>
              <label className="sr-only" htmlFor="repository">
                Ссылка на GitHub-репозиторий
              </label>
              <input
                id="repository"
                type="url"
                value={repo}
                onChange={(event) => {
                  setRepo(event.target.value);
                  if (buildState !== 'idle') setBuildState('idle');
                }}
                placeholder="https://github.com/username/project"
                autoComplete="url"
              />
              <button type="submit" disabled={buildState === 'building'}>
                {buildState === 'building' ? 'Building…' : 'Build'}
                <span aria-hidden="true">→</span>
              </button>
            </div>
            <div className="form-meta" aria-live="polite">
              <span
                className={
                  buildState === 'error'
                    ? 'error-text'
                    : buildState === 'ready'
                      ? 'success-text'
                      : ''
                }
              >
                {buildState === 'error' && 'Вставьте полную ссылку на GitHub-репозиторий'}
                {buildState === 'building' && 'AI анализирует структуру проекта…'}
                {buildState === 'ready' && 'Конфигурация распознана. Можно запускать деплой.'}
                {buildState === 'idle' && 'Публичный репозиторий · без настройки конфигов'}
              </span>
              <span>Первый деплой бесплатно</span>
            </div>
          </form>

          <div className="deploy-visual" aria-label="Пример процесса деплоя">
            <div className="visual-topbar">
              <div className="window-dots">
                <i />
                <i />
                <i />
              </div>
              <span>deploy / snaphost-app</span>
              <span className="secure-label">● secure</span>
            </div>
            <div className="visual-body">
              <div className="pipeline">
                <div className="pipeline-line">
                  <span className="line-progress" />
                </div>
                <div className="pipeline-step is-complete">
                  <span className="step-node">01</span>
                  <div>
                    <b>Repository</b>
                    <small>main · 8ac2f1</small>
                  </div>
                  <em>Connected</em>
                </div>
                <div className="pipeline-step is-active">
                  <span className="step-node">02</span>
                  <div>
                    <b>AI analysis</b>
                    <small>Next.js · Node 22</small>
                  </div>
                  <em>0.8 sec</em>
                </div>
                <div className="pipeline-step">
                  <span className="step-node">03</span>
                  <div>
                    <b>Edge deploy</b>
                    <small>Amsterdam · Warsaw</small>
                  </div>
                  <em>Ready</em>
                </div>
              </div>
              <div className="mini-console" aria-hidden="true">
                <div>
                  <span>12:04:02</span> Cloning repository
                </div>
                <div>
                  <span>12:04:03</span> Framework detected: Next.js
                </div>
                <div>
                  <span>12:04:04</span> Dependencies cached <b>+43%</b>
                </div>
                <div className="console-success">
                  <span>12:04:07</span> Deployment ready ✓
                </div>
              </div>
            </div>
          </div>
        </div>

        <div className="hero-bottom shell">
          <span>01 — DEPLOY WITHOUT CONFIG</span>
          <a href="#platform">
            Узнать, как это работает <span>↓</span>
          </a>
        </div>
      </section>

      <div className="platform-wrap" id="platform">
        <section className="platform-intro shell">
          <div className="section-kicker">02 / Платформа</div>
          <div className="intro-grid">
            <h2>
              Инфраструктура,
              <br />
              которая понимает код.
            </h2>
            <p>
              Snaphost убирает рутину между последним коммитом и работающим приложением. Не нужно
              выбирать образ, писать CI/CD или вручную подключать SSL — платформа принимает
              технические решения сама.
            </p>
          </div>
        </section>

        <section className="workflow shell" id="workflow">
          <div className="workflow-card workflow-main">
            <div className="workflow-copy">
              <span className="card-number">01</span>
              <div>
                <h3>Ссылка — это вся настройка</h3>
                <p>
                  Подключите GitHub. AI изучит package.json, зависимости, команды сборки и структуру
                  проекта.
                </p>
              </div>
            </div>
            <div className="code-map" aria-hidden="true">
              <div className="file-tree">
                <span className="tree-title">snaphost-app</span>
                <span>⌞ app</span>
                <span className="tree-active">&nbsp;&nbsp;◇ page.tsx</span>
                <span>⌞ public</span>
                <span>&nbsp;&nbsp;{'{}'} package.json</span>
                <span>&nbsp;&nbsp;# README.md</span>
              </div>
              <div className="scan-panel">
                <div className="scan-line" />
                <span>Analyzing</span>
                <strong>Next.js 15</strong>
                <small>SSR · API routes · Static assets</small>
                <div className="confidence">
                  <i style={{ width: '96%' }} />
                  <span>96% confidence</span>
                </div>
              </div>
            </div>
          </div>

          <div className="workflow-card">
            <div className="workflow-copy">
              <span className="card-number">02</span>
              <div>
                <h3>Сборка без сюрпризов</h3>
                <p>Изолированное окружение, кеш зависимостей и понятные логи в реальном времени.</p>
              </div>
            </div>
            <div className="metric-visual" aria-hidden="true">
              <div className="metric-orbit">
                <span />
                <span />
                <span />
              </div>
              <div>
                <strong>7.4s</strong>
                <small>build time</small>
              </div>
            </div>
          </div>

          <div className="workflow-card">
            <div className="workflow-copy">
              <span className="card-number">03</span>
              <div>
                <h3>Готово для пользователей</h3>
                <p>Технический домен, HTTPS и доставка контента с ближайшей edge-точки.</p>
              </div>
            </div>
            <div className="edge-visual" aria-hidden="true">
              <span className="edge-center">S</span>
              <i className="edge-line e1" />
              <i className="edge-line e2" />
              <i className="edge-line e3" />
              <span className="edge-point p1">AMS</span>
              <span className="edge-point p2">WAW</span>
              <span className="edge-point p3">FRA</span>
            </div>
          </div>
        </section>

        <section className="stack-section shell">
          <div className="stack-copy">
            <span className="section-kicker">Поддерживаемые стеки</span>
            <h2>
              Ваш фреймворк
              <br />
              уже знаком Snaphost.
            </h2>
            <p>Автоопределение команд, переменных окружения и оптимального сценария сборки.</p>
          </div>
          <div className="stack-grid">
            {stacks.map((stack) => (
              <div className="stack-item" key={stack.name}>
                <span
                  className="stack-mark"
                  style={{ color: stack.color, borderColor: `${stack.color}55` }}
                >
                  {stack.mark}
                </span>
                <div>
                  <b>{stack.name}</b>
                  <small>Auto-detected</small>
                </div>
                <span className="stack-check">✓</span>
              </div>
            ))}
          </div>
        </section>
      </div>

      <section className="pricing shell" id="pricing">
        <div className="pricing-heading">
          <div>
            <span className="section-kicker">03 / Тарифы</span>
            <h2>
              Начните бесплатно.
              <br />
              Растите без ограничений.
            </h2>
          </div>
          <p>Выберите ресурсы под текущую задачу. Перейти на другой тариф можно в любой момент.</p>
        </div>
        <div className="pricing-grid">
          {plans.map((plan, index) => (
            <article className={`price-card ${plan.featured ? 'featured' : ''}`} key={plan.name}>
              {plan.featured && <span className="popular-badge">Популярный</span>}
              <div className="plan-head">
                <span className="plan-index">SN / {String(index + 1).padStart(2, '0')}</span>
                <h3>{plan.name}</h3>
                <span className="plan-label">{plan.label}</span>
                <p>{plan.description}</p>
              </div>
              <Link className="plan-button" to="/signup">
                Выбрать {plan.name}
                <span>→</span>
              </Link>
              <ul>
                {plan.features.map((feature) => (
                  <li key={feature}>
                    <span>✓</span>
                    {feature}
                  </li>
                ))}
              </ul>
            </article>
          ))}
        </div>
      </section>

      <section className="final-cta">
        <div className="shell final-cta-inner">
          <span className="section-kicker">Ваш следующий релиз</span>
          <h2>
            Отправьте код
            <br />в сеть за секунды.
          </h2>
          <Link to="/signup">
            Запустить проект <span>↗</span>
          </Link>
        </div>
      </section>

      <footer id="docs">
        <div className="shell footer-grid">
          <div className="footer-brand">
            <BrandLogo />
            <p>Мгновенный деплой веб-приложений с AI-анализом кода.</p>
          </div>
          <div className="footer-column">
            <b>Документы</b>
            <Link to="/legal/offer">Оферта</Link>
            <Link to="/legal/privacy">Персональные данные</Link>
            <Link to="/legal/refunds">Оплата и возврат</Link>
            <Link to="/legal/acceptable-use">Правила использования</Link>
            <Link to="/legal">Все документы</Link>
          </div>
          <div className="footer-column">
            <b>Связаться</b>
            <a href="mailto:support@snaphost.ru">support@snaphost.ru</a>
            <a href="mailto:report@snaphost.ru">report@snaphost.ru</a>
            <Link to="/legal/abuse">Сообщить о нарушении</Link>
          </div>
          <div className="footer-column">
            <b>Компания</b>
            <Link to="/legal/details">Реквизиты</Link>
            <a href="#status">Статус платформы</a>
          </div>
        </div>
        <div className="shell footer-bottom">
          <span>© {new Date().getFullYear()} Snaphost</span>
          <span>BUILD / SHIP / SCALE</span>
          <span>RU · EN</span>
        </div>
      </footer>
    </main>
  );
}
