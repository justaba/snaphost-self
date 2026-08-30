import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    // Straight into the Go package that embeds it. go:embed cannot reach
    // outside its own module, so the assets have to land inside the tree the
    // binary is built from rather than beside the sources that produce them.
    outDir: fileURLToPath(new URL('../internal/panel/dist', import.meta.url)),
    // Not emptied: the directory holds a committed .gitkeep, which is what
    // lets go:embed compile before anyone has built the panel. Wiping it on
    // every build would dirty the tree each time the frontend is rebuilt.
    emptyOutDir: false,
  },
  server: {
    // Same-origin in development, because the session is an HttpOnly cookie
    // with SameSite=Lax: a dev server on another port would be a different
    // site, and the browser would withhold the cookie on every request that is
    // not a top-level navigation. Proxying makes development match production
    // rather than requiring a second, looser cookie policy to work around it.
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./tests/setup.ts'],
    css: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.d.ts'],
    },
  },
});
