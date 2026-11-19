import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { readFileSync } from 'node:fs';
const version = process.env.APP_VERSION || JSON.parse(readFileSync(new URL('../VERSION.json', import.meta.url), 'utf8')).version;
export default defineConfig({ plugins: [vue(), {
    name: 'echat-client-entries',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const path = req.url?.split('?')[0];
        if (path === '/agent') req.url = '/agent.html';
        if (path === '/customer') req.url = '/index.html';
        next();
      });
    }
  }], build: { rollupOptions: { input: ['index.html', 'agent.html'] } }, define: { __WEB_VERSION__: JSON.stringify(version) }, server: { proxy: { '/api': 'http://127.0.0.1:8090' } } });
