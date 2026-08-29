import styles from './AdminTransactionsPage.module.css';

import { AdminTransactionList } from '@/widgets/admin-transaction-list';

function AdminTransactionsPage() {
  return (
    <div className={styles.root}>
      <AdminTransactionList />
    </div>
  );
}

export default AdminTransactionsPage;
