import styles from './TopBar.module.css';

import { useEffect, useRef, useState } from 'react';
import { LogOut, Menu } from 'lucide-react';
import { signOut, useAuth, useSessionDispatch } from '@/entities/session';

export interface TopBarProps {
  onMenuClick?: () => void;
}

function TopBar({ onMenuClick }: TopBarProps) {
  const { user } = useAuth();
  const dispatch = useSessionDispatch();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!menuOpen) return;
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [menuOpen]);

  const handleSignOut = () => {
    setMenuOpen(false);
    void dispatch(signOut());
  };

  const initial = user?.email?.[0] ?? '?';

  return (
    <header
      className={`${styles.root} h-16 bg-white border-b border-zinc-200 flex items-center justify-between px-4 md:px-6 sticky top-0 z-30`}
    >
      <div className="flex items-center gap-3">
        {onMenuClick && (
          <button
            type="button"
            onClick={onMenuClick}
            aria-label="Открыть меню"
            className="md:hidden p-2 rounded-md text-zinc-600 hover:text-zinc-900 hover:bg-zinc-100 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300"
          >
            <Menu size={20} />
          </button>
        )}
      </div>

      <div className="flex items-center gap-4">
        <div className="relative" ref={menuRef}>
          <button
            type="button"
            onClick={() => setMenuOpen((v) => !v)}
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            className="flex items-center gap-2 p-1 rounded-full hover:bg-zinc-100 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300"
          >
            <span className="h-8 w-8 rounded-full bg-zinc-900 text-white flex items-center justify-center text-sm font-medium uppercase">
              {initial}
            </span>
          </button>
          {menuOpen && (
            <div
              role="menu"
              className="absolute right-0 mt-2 w-56 bg-white border border-zinc-200 rounded-lg shadow-md py-1 z-40"
            >
              <div className="px-3 py-2 border-b border-zinc-100">
                <div className="text-sm font-medium text-zinc-900 truncate">{user?.email}</div>
                <div className="text-xs text-zinc-500 uppercase tracking-wide">{user?.role}</div>
              </div>
              <button
                role="menuitem"
                type="button"
                onClick={handleSignOut}
                className="w-full flex items-center gap-2 px-3 py-2 text-sm text-zinc-700 hover:bg-zinc-50 transition-colors"
              >
                <LogOut size={16} />
                Выйти
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}

export default TopBar;
