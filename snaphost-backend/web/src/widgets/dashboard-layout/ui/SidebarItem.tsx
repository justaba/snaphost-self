import styles from './SidebarItem.module.css';

import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';

export interface SidebarItemProps {
  to: string;
  icon: ReactNode;
  label: string;
  onNavigate?: () => void;
}

function SidebarItem({ to, icon, label, onNavigate }: SidebarItemProps) {
  return (
    <NavLink
      to={to}
      end
      onClick={onNavigate}
      className={({ isActive }) =>
        [
          'flex items-center gap-3 h-10 px-3 rounded-lg text-sm transition-colors duration-150',
          'focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300',
          isActive
            ? 'bg-zinc-100 text-zinc-900 font-semibold'
            : 'text-zinc-600 hover:bg-zinc-50 hover:text-zinc-900 font-medium',
        ].join(' ')
      }
    >
      <span className={`${styles.root} inline-flex shrink-0`}>{icon}</span>
      <span>{label}</span>
    </NavLink>
  );
}

export default SidebarItem;
