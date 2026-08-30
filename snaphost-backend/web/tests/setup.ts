import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

vi.stubEnv('VITE_API_URL', 'http://localhost:8080');

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
