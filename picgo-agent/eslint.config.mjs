// ESLint 扁平配置（ESLint 9）。
// 风格参考 PicGo-Core 的 eslint.config.js：Standard 取向（2 空格、单引号、无分号由不做强制）
// 但不引入 prettier（agent 代码量小，避免额外依赖）。

import js from '@eslint/js'
import globals from 'globals'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import tsParser from '@typescript-eslint/parser'

export default [
  {
    ignores: ['dist/**', 'node_modules/**', '*.cjs', '*.mjs']
  },

  // ---- JS 配置文件 ----
  {
    ...js.configs.recommended,
    files: ['**/*.js'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: { ...globals.node }
    }
  },

  // ---- TypeScript 源码 ----
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 2023,
        sourceType: 'module',
        project: './tsconfig.json'
      },
      globals: { ...globals.node, ...globals.es2023 }
    },
    plugins: {
      '@typescript-eslint': tsPlugin
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,

      // 类型安全：明确禁止 any（项目规范：不写 as any）
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' }
      ],
      '@typescript-eslint/consistent-type-imports': [
        'error',
        { prefer: 'type-imports', fixStyle: 'inline-type-imports' }
      ],
      '@typescript-eslint/no-non-null-assertion': 'warn',
      '@typescript-eslint/require-await': 'off', // Hono 的 handler 常需 async 签名
      '@typescript-eslint/no-misused-promises': [
        'error',
        { checksVoidReturn: false }
      ],

      // 通用质量
      'no-console': ['error', { allow: ['error'] }],
      eqeqeq: ['error', 'always'],
      'prefer-const': 'error',
      'no-var': 'error',
      'object-shorthand': ['error', 'always'],
      'no-throw-literal': 'off', // 由 @typescript-eslint 的版本接管（见下）
      '@typescript-eslint/only-throw-error': 'error'
    }
  },

  // ---- 测试文件放宽 ----
  {
    files: ['**/*.test.ts', '**/__tests__/**/*.ts'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-non-null-assertion': 'off',
      'no-console': 'off'
    }
  }
]
