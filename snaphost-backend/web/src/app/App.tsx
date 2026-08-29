import styles from './App.module.css';

import { lazy, Suspense } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';

import { ToastProvider } from '@/shared/ui/toast';
import { AuthProvider } from './providers/AuthProvider';
import { queryClient } from './providers/query-client';
import ProtectedRoute from './routes/ProtectedRoute';

const MainLayout = lazy(() =>
  import('@/widgets/marketing-layout').then(({ MarketingLayout }) => ({
    default: MarketingLayout,
  })),
);
const Home = lazy(() => import('@/pages/home').then(({ HomePage }) => ({ default: HomePage })));
const LoginPage = lazy(() =>
  import('@/pages/login').then(({ LoginPage: Page }) => ({ default: Page })),
);
const SignupPage = lazy(() =>
  import('@/pages/signup').then(({ SignupPage: Page }) => ({ default: Page })),
);
const RecoverPage = lazy(() =>
  import('@/pages/recover').then(({ RecoverPage: Page }) => ({ default: Page })),
);
const ResetPasswordPage = lazy(() =>
  import('@/pages/reset-password').then(({ ResetPasswordPage: Page }) => ({ default: Page })),
);
const AuthCallbackPage = lazy(() =>
  import('@/pages/auth-callback').then(({ AuthCallbackPage: Page }) => ({ default: Page })),
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
const StatsPage = lazy(() =>
  import('@/pages/stats').then(({ StatsPage: Page }) => ({ default: Page })),
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
const AdminTransactionsPage = lazy(() =>
  import('@/pages/admin-transactions').then(({ AdminTransactionsPage: Page }) => ({
    default: Page,
  })),
);
const AdminDomainsPage = lazy(() =>
  import('@/pages/admin-domains').then(({ AdminDomainsPage: Page }) => ({ default: Page })),
);
const LegalIndexPage = lazy(() =>
  import('@/pages/legal-index').then(({ LegalIndexPage: Page }) => ({ default: Page })),
);
const LegalDocumentPage = lazy(() =>
  import('@/pages/legal-document').then(({ LegalDocumentPage: Page }) => ({ default: Page })),
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
                <Route element={<MainLayout />}>
                  <Route path="/" element={<Home />} />
                  <Route path="/legal" element={<LegalIndexPage />} />
                  <Route path="/legal/:documentSlug" element={<LegalDocumentPage />} />
                  <Route path="/abuse" element={<Navigate to="/legal/abuse" replace />} />
                </Route>

                <Route path="/login" element={<LoginPage />} />
                <Route path="/signup" element={<SignupPage />} />
                <Route path="/recover" element={<RecoverPage />} />
                <Route path="/reset-password" element={<ResetPasswordPage />} />
                <Route path="/auth/callback" element={<AuthCallbackPage />} />

                <Route element={<ProtectedRoute />}>
                  <Route path="/dashboard" element={<DashboardLayout />}>
                    <Route index element={<Navigate to="projects" replace />} />
                    <Route path="projects" element={<ProjectsPage />} />
                    <Route path="projects/:projectId" element={<ProjectPage />} />
                    <Route path="stats" element={<StatsPage />} />
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
                        <Route path="transactions" element={<AdminTransactionsPage />} />
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
