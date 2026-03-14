import { defineConfig } from 'vite';
import { getWebAPIOrigin, loadConfigSync } from './scripts/config.mjs';

const apiOrigin = process.env.REPOSVIEW_API_ORIGIN || getWebAPIOrigin(loadConfigSync());

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
