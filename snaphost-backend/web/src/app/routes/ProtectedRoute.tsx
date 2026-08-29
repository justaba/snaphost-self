import styles from './ProtectedRoute.module.css';

import { useEffect, type ReactNode } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router-dom';

import { useAuth, type User } from '@/entities/session';

interface ProtectedRouteProps {
  children?: ReactNode;
  requiredRole?: User['role'];
}

export function ProtectedRoute({ children, requiredRole }: ProtectedRouteProps) {
  const { status, user } = useAuth();
  const location = useLocation();

  useEffect(() => {
    if (status === 'authenticated' && requiredRole && user && user.role !== requiredRole) {
      console.warn(`Access denied: route requires role "${requiredRole}".`);
    }
  }, [status, user, requiredRole]);

  if (status === 'idle' || status === 'loading') {
    return (
      <div className={`${styles.root} min-h-screen flex items-center justify-center bg-slate-50`}>
        <div className="animate-spin w-8 h-8 border-4 border-violet-500 border-t-transparent rounded-full" />
      </div>
    );
  }

  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }

  if (requiredRole && user && user.role !== requiredRole) {
    return <Navigate to="/" replace />;
  }

  return <>{children ?? <Outlet />}</>;
}

export default ProtectedRoute;
