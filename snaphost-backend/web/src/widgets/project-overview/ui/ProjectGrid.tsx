import type { DeploySummary } from '@/entities/deploy';
import ProjectCard from './ProjectCard';
import styles from './ProjectGrid.module.css';

export interface ProjectGridProps {
  deploys: DeploySummary[];
  onOpen: (id: string) => void;
}

function ProjectGrid({ deploys, onOpen }: ProjectGridProps) {
  return (
    <div className={styles.root}>
      {deploys.map((deploy) => (
        <ProjectCard key={deploy.id} deploy={deploy} onOpen={onOpen} />
      ))}
    </div>
  );
}

export default ProjectGrid;
