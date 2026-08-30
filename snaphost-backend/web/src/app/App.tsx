import styles from './App.module.css';

import { lazy, Suspense } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';

import { ToastProvider } from '@/shared/ui/toast';
import { AuthProvider } from './providers/AuthProvider';
import { queryClient } from './providers/query-client';
import ProtectedRoute from './routes/ProtectedRoute';

const LoginPage = lazy(() =>
  import('@/pages/login').then(({ LoginPage: Page }) => ({ default: Page })),
);
const DashboardLayout = lazy(() =>
  import('@/widgets/dashboard-layout').then(({ DashboardLayout: Layout }) => ({ default: Layout })),
);
const ProjectsPage = lazy(() =>
  import('@/pages/projects').then(({ ProjectsPage: Page }) => ({ default: Page })),
);
const ProjectPage = lazy(() =>
  import('@/pages/project-details').then(({ ProjectDetailsPage }) => ({
    default: ProjectDetailsPage,
  })),
);
const ApiKeysPage = lazy(() =>
  import('@/pages/api-keys').then(({ ApiKeysPage: Page }) => ({ default: Page })),
);
const DomainsPage = lazy(() =>
  import('@/pages/domains').then(({ DomainsPage: Page }) => ({ default: Page })),
);
const SettingsPage = lazy(() =>
  import('@/pages/settings').then(({ SettingsPage: Page }) => ({ default: Page })),
);
const AdminLayout = lazy(() =>
  import('@/widgets/admin-layout').then(({ AdminLayout: Layout }) => ({ default: Layout })),
);
const AdminOverviewPage = lazy(() =>
  import('@/pages/admin-overview').then(({ AdminOverviewPage: Page }) => ({ default: Page })),
);
const AdminUsersPage = lazy(() =>
  import('@/pages/admin-users').then(({ AdminUsersPage: Page }) => ({ default: Page })),
);
const AdminUserPage = lazy(() =>
  import('@/pages/admin-user').then(({ AdminUserPage: Page }) => ({ default: Page })),
);
const AdminDeploysPage = lazy(() =>
  import('@/pages/admin-deploys').then(({ AdminDeploysPage: Page }) => ({ default: Page })),
);
const AdminDeployPage = lazy(() =>
  import('@/pages/admin-deploy').then(({ AdminDeployPage: Page }) => ({ default: Page })),
);
const AdminDomainsPage = lazy(() =>
  import('@/pages/admin-domains').then(({ AdminDomainsPage: Page }) => ({ default: Page })),
);

function RouteFallback() {
  return (
    <div
      className={`${styles.root} flex min-h-screen items-center justify-center bg-zinc-50`}
      role="status"
    >
      <span className="h-8 w-8 animate-spin rounded-full border-4 border-zinc-300 border-t-zinc-900" />
      <span className="sr-only">Загрузка страницы…</span>
    </div>
  );
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router>
        <AuthProvider>
          <ToastProvider>
            <Suspense fallback={<RouteFallback />}>
              <Routes>
                {/* The panel has no public face. An operator arriving at the root
                    is either signed in, in which case this is their dashboard,
                    or not, in which case ProtectedRoute sends them to login.
                    What used to be here was a marketing landing page with
                    pricing tiers, which is a page for selling a service to
                    someone who does not own the machine. */}
                <Route path="/" element={<Navigate to="/dashboard" replace />} />

                <Route path="/login" element={<LoginPage />} />

                <Route element={<ProtectedRoute />}>
                  <Route path="/dashboard" element={<DashboardLayout />}>
                    <Route index element={<Navigate to="projects" replace />} />
                    <Route path="projects" element={<ProjectsPage />} />
                    <Route path="projects/:projectId" element={<ProjectPage />} />
                    <Route path="keys" element={<ApiKeysPage />} />
                    <Route path="domains" element={<DomainsPage />} />
                    <Route path="settings" element={<SettingsPage />} />

                    {/* Operator screens. The role gate here only hides the UI —
                      api-gateway's Casbin policy and user-billing's own role
                      check are what actually refuse the data. */}
                    <Route element={<ProtectedRoute requiredRole="admin" />}>
                      <Route path="admin" element={<AdminLayout />}>
                        <Route index element={<AdminOverviewPage />} />
                        <Route path="users" element={<AdminUsersPage />} />
                        <Route path="users/:userId" element={<AdminUserPage />} />
                        <Route path="deploys" element={<AdminDeploysPage />} />
                        <Route path="deploys/:deployId" element={<AdminDeployPage />} />
                        <Route path="domains" element={<AdminDomainsPage />} />
                      </Route>
                    </Route>
                  </Route>
                </Route>
              </Routes>
            </Suspense>
          </ToastProvider>
        </AuthProvider>
      </Router>
    </QueryClientProvider>
  );
}

export default App;
