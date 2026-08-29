import styles from './MarketingLayout.module.css';

import { Outlet } from 'react-router-dom';

import { ScrollToTopButton } from '@/shared/ui/scroll-to-top';

export default function MainLayout() {
  return (
    <div className={styles.root}>
      <Outlet />
      <ScrollToTopButton />
    </div>
  );
}
