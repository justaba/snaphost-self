import styles from './Skeleton.module.css';

export interface SkeletonProps {
  className?: string;
}

function Skeleton({ className = '' }: SkeletonProps) {
  return (
    <div
      aria-hidden="true"
      className={`${styles.root} animate-pulse bg-zinc-200/80 rounded-md ${className}`}
    />
  );
}

export default Skeleton;
