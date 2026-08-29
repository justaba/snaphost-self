import acceptableUse from './content/acceptable-use.md?raw';
import abuse from './content/abuse.md?raw';
import consent from './content/consent.md?raw';
import cookies from './content/cookies.md?raw';
import dataProcessing from './content/data-processing.md?raw';
import details from './content/details.md?raw';
import offer from './content/offer.md?raw';
import privacy from './content/privacy.md?raw';
import refunds from './content/refunds.md?raw';
export { LEGAL_VERSION } from './version';

export interface LegalDocument {
  slug: string;
  title: string;
  description: string;
  source: string;
}

export const legalDocuments: LegalDocument[] = [
  {
    slug: 'offer',
    title: 'Публичная оферта',
    description: 'Условия договора, подписки, коинов и использования платформы.',
    source: offer,
  },
  {
    slug: 'privacy',
    title: 'Политика обработки персональных данных',
    description: 'Цели, состав, сроки и порядок обработки персональных данных.',
    source: privacy,
  },
  {
    slug: 'consent',
    title: 'Согласие на обработку персональных данных',
    description: 'Отдельное согласие, которое пользователь даёт при регистрации.',
    source: consent,
  },
  {
    slug: 'acceptable-use',
    title: 'Правила допустимого использования',
    description: 'Запрещённый контент, безопасность и меры реагирования.',
    source: acceptableUse,
  },
  {
    slug: 'refunds',
    title: 'Оплата, автопродление и возврат',
    description: 'Отмена подписки, отказ от автоплатежей и расчёт возврата.',
    source: refunds,
  },
  {
    slug: 'cookies',
    title: 'Cookie и локальное хранилище',
    description: 'Необходимые технологии браузера и управление ими.',
    source: cookies,
  },
  {
    slug: 'data-processing',
    title: 'Обработка данных клиентов',
    description: 'Поручение на обработку данных посетителей пользовательских приложений.',
    source: dataProcessing,
  },
  {
    slug: 'abuse',
    title: 'Сообщения о нарушениях',
    description: 'Как сообщить о незаконном контенте пользовательского приложения.',
    source: abuse,
  },
  {
    slug: 'details',
    title: 'Реквизиты и контакты',
    description: 'Сведения об исполнителе, адреса поддержки и приёма жалоб.',
    source: details,
  },
];

export function getLegalDocument(slug: string | undefined): LegalDocument | undefined {
  return legalDocuments.find((document) => document.slug === slug);
}
