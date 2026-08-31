import styles from './Sidebar.module.css';

import { LayoutGrid, Globe, KeyRound, Settings } from 'lucide-react';
import SidebarItem from './SidebarItem';

const VERSION = (import.meta.env.VITE_APP_VERSION as string | undefined) ?? '0.1.0';

export interface SidebarProps {
  onNavigate?: () => void;
}

/** There is no Администрирование entry any more, and no role check to decide
 *  whether to draw one. The operator console was a second copy of these
 *  screens for someone administering other people's accounts; a self-hosted
 *  install has one account, so this navigation is the console. */
function Sidebar({ onNavigate }: SidebarProps) {
  return (
    <aside className={`${styles.root} w-60 h-full bg-white border-r border-zinc-200 flex flex-col`}>
      <div className="h-16 flex items-center px-6 border-b border-zinc-200 shrink-0">
        <span className="text-lg font-semibold tracking-tight text-zinc-900">SnapHost</span>
      </div>
      <nav className="flex-1 overflow-y-auto px-3 py-4 flex flex-col gap-1">
        <SidebarItem
          to="/dashboard/projects"
          icon={<LayoutGrid size={18} />}
          label="Проекты"
          onNavigate={onNavigate}
        />
        <SidebarItem
          to="/dashboard/domains"
          icon={<Globe size={18} />}
          label="Домены"
          onNavigate={onNavigate}
        />
        <SidebarItem
          to="/dashboard/keys"
          icon={<KeyRound size={18} />}
          label="API-ключи"
          onNavigate={onNavigate}
        />
        <SidebarItem
          to="/dashboard/settings"
          icon={<Settings size={18} />}
          label="Настройки"
          onNavigate={onNavigate}
        />
      </nav>
      <div className="px-6 py-4 border-t border-zinc-200 text-xs text-zinc-400 shrink-0">
        v{VERSION}
      </div>
    </aside>
  );
}

export default Sidebar;
