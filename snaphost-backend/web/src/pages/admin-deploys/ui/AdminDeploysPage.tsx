import styles from './AdminDeploysPage.module.css';

import { AdminDeployList } from '@/widgets/admin-deploy-list';

function AdminDeploysPage() {
  return (
    <div className={styles.root}>
      <AdminDeployList />
    </div>
  );
}

export default AdminDeploysPage;
