import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const environment = loadEnv(mode, process.cwd(), 'API_');
  const target = environment.API_PROXY_TARGET || 'http://localhost:8000';
  const proxy = { target, changeOrigin: true, headers: { Origin: new URL(target).origin } };
  return {
  plugins: [react()],
  server: {
    proxy: {
      '/api': proxy,
      '/auth': proxy,
    },
  },
  test: { environment: 'jsdom', setupFiles: ['./src/test/setup.ts'], restoreMocks: true },
  };
});
