import { defineConfig } from 'vite';

const apiOrigin = process.env.REPOSVIEW_API_ORIGIN || 'http://127.0.0.1:8787';

export default defineConfig({
  root: 'web',
  server: {
    proxy: {
      '/sync': apiOrigin,
      '/sync-status': apiOrigin,
      '/rows': apiOrigin,
      '/repo-details': apiOrigin,
      '/actions': apiOrigin,
      '/events': apiOrigin,
      '/healthz': apiOrigin
    }
  }
});
