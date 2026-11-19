import path from 'node:path';
import babel from '@rolldown/plugin-babel';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { compression, defineAlgorithm } from 'vite-plugin-compression2';

export default defineConfig({
  base: './',
  plugins: [
    react(),
    babel({ presets: [reactCompilerPreset()] }),
    compression({
      algorithms: [defineAlgorithm('gzip', { level: 9 })],
      include: /\.(html|css|js|mjs|json|svg|txt|xml)$/,
      deleteOriginalAssets: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, '../static/out'),
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // 第三方库单独成块: 业务代码每次发版都会变, 但 React/图表/动效这些依赖不变,
        // 分包后浏览器可以继续命中旧缓存, 不必随每次发版重下。
        // 只列真正的大件(recharts 与 d3 系列、motion), 其余交给默认按需拆分的页面块。
        // rolldown-vite 只接受函数形式(对象形式会报类型错误)。
        // 只拆「首屏必然要、且发版不变」的 React 运行时: 它独立成块后能跨发版命中缓存。
        // 不要把 recharts/motion 这类只被某个页面用到的库也手动拆出来 —— 手动分块会把它
        // 提到入口的预加载清单里, 结果是没打开图表页的用户也要先下 108K 的图表库。
        manualChunks: (id: string) =>
          /node_modules\/(react|react-dom|scheduler)\//.test(id) ? 'react' : undefined,
      },
    },
  },
  server: {
    hmr: process.env.DISABLE_HMR !== 'true',
    watch: process.env.DISABLE_HMR === 'true' ? null : {},
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
});
