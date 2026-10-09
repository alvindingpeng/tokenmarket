import path from 'node:path';
import { defineConfig } from 'vitest/config';

// 单测与 vite.config.ts 分开配置: vitest.config.ts 会被优先加载,
// 所以 @ 别名必须在这里重复一遍, 否则测试里 import '@/...' 会解析失败。
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  test: {
    environment: 'node', // 被测对象是纯函数与 URL 构造, 不需要 jsdom。
    include: ['src/**/*.test.ts'],
  },
});
