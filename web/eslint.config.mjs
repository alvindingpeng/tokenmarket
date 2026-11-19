import js from '@eslint/js';
import { defineConfig } from 'eslint/config';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

export default defineConfig([
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
      // 类型感知: 只为 no-floating-promises 这类需要推断返回类型的规则开启。
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      // 未被 await 的 Promise 会让失败静默消失(前端最常见的「点了没反应」来源)。
      // 有意不等待时显式写 void, 让「我知道这是 fire-and-forget」成为代码里的事实。
      '@typescript-eslint/no-floating-promises': 'error',
      // 变体样式、共享 store 与组件同文件导出是既有约定，这些导出不参与 Fast Refresh 组件替换。
      'react-refresh/only-export-components': ['error', {
        allowExportNames: [
          'badgeVariants',
          'buttonVariants',
          'tabsListVariants',
          'useMorphingDialog',
          'usePageActionsStore',
          'useRuntimeClock',
          'useTheme',
        ],
      }],
    },
  },
]);
