import styles from './AdminLayout.module.css';

import { NavLink, Outlet } from 'react-router-dom';

const TABS = [
  { to: '/dashboard/admin', label: 'Сводка', end: true },
  { to: '/dashboard/admin/users', label: 'Пользователи', end: false },
  { to: '/dashboard/admin/deploys', label: 'Деплои', end: false },
  { to: '/dashboard/admin/transactions', label: 'Транзакции', end: false },
  { to: '/dashboard/admin/domains', label: 'Домены', end: false },
];

/** Frame for the operator screens. Everything under it is read-only — there is
 *  no action here that changes a user's state, deliberately: those need an
 *  audit trail that does not exist yet. */
function AdminLayout() {
  return (
    <div className={`${styles.root} max-w-7xl mx-auto`}>
      <header className="mb-5">
        <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Администрирование</h1>
        <p className="text-sm text-zinc-500 mt-1">
          Полная картина по платформе: аккаунты, деплои, счета и домены. Только просмотр.
        </p>
      </header>

      <nav className="flex gap-1 border-b border-zinc-200 mb-5 overflow-x-auto">
        {TABS.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            end={tab.end}
            className={({ isActive }) =>
              [
                '-mb-px px-3 py-2 text-sm whitespace-nowrap border-b-2 transition-colors',
                isActive
                  ? 'border-zinc-900 text-zinc-900 font-medium'
                  : 'border-transparent text-zinc-500 hover:text-zinc-800',
              ].join(' ')
            }
          >
            {tab.label}
          </NavLink>
        ))}
      </nav>

      <Outlet />
    </div>
  );
}

export default AdminLayout;
