import { defineConfig } from 'vite';
import preact from '@preact/preset-vite';

// 产物直接写进 ../web，由 Go 的 //go:embed web 打进二进制。文件名固定不带 hash：
// 产物要提交进仓库，CI 的 studio-ui job 重新构建后 git diff 必须为空，带 hash 会让每次改动都换文件名。
export default defineConfig({
  plugins: [preact()],
  base: '/',
  build: {
    outDir: '../web',
    emptyOutDir: true,
    assetsDir: 'assets',
    sourcemap: false,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/app.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
  server: {
    // npm run dev 时把接口转给本机 am studio（默认 127.0.0.1:4321）。
    proxy: {
      '/api': 'http://127.0.0.1:4321',
      '/preview': 'http://127.0.0.1:4321',
    },
  },
});
