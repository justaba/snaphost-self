import styles from './DashboardLayout.module.css';

import { useEffect, useState } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '@/entities/session';
import { Spinner } from '@/shared/ui/spinner';
import Sidebar from './Sidebar';
import TopBar from './TopBar';

function DashboardLayout() {
  const { status } = useAuth();
  const location = useLocation();
  const [drawerOpen, setDrawerOpen] = useState(false);

  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  if (status === 'idle' || status === 'loading') {
    return (
      <div className={`${styles.root} min-h-screen flex items-center justify-center bg-zinc-50`}>
        <Spinner size="lg" />
      </div>
    );
  }

  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }

  return (
    <div className="min-h-screen bg-zinc-50 text-zinc-900 flex">
      <div className="hidden md:block w-60 shrink-0 sticky top-0 h-screen">
        <Sidebar />
      </div>

      {drawerOpen && (
        <div className="md:hidden fixed inset-0 z-40 flex" role="dialog" aria-modal="true">
          <button
            type="button"
            aria-label="Закрыть меню"
            onClick={() => setDrawerOpen(false)}
            className="absolute inset-0 bg-zinc-900/40"
          />
          <div className="relative w-60 h-full bg-white shadow-xl">
            <Sidebar onNavigate={() => setDrawerOpen(false)} />
          </div>
        </div>
      )}

      <div className="flex-1 flex flex-col min-w-0">
        <TopBar onMenuClick={() => setDrawerOpen(true)} />
        <main className="flex-1 overflow-x-hidden overflow-y-auto p-4 md:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export default DashboardLayout;
