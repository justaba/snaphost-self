import js from '@eslint/js';
import eslintConfigPrettier from 'eslint-config-prettier';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

const publicApiBoundary = {
  group: [
    '@/pages/*/{api,config,lib,model,ui}/**',
    '@/widgets/*/{api,config,lib,model,ui}/**',
    '@/features/*/{api,config,lib,model,ui}/**',
    '@/entities/*/{api,config,lib,model,ui}/**',
  ],
  message: 'Import the slice through its public index.ts API.',
};

const restrictedImports = (...patterns) => [
  'error',
  { patterns: [publicApiBoundary, ...patterns] },
];

export default tseslint.config(
  {
    ignores: ['coverage', 'dist', 'node_modules'],
  },
  {
    ...js.configs.recommended,
    files: ['**/*.{js,mjs,cjs}'],
  },
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}', 'tests/**/*.{ts,tsx}'],
    languageOptions: {
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      '@typescript-eslint/consistent-type-imports': ['error', { fixStyle: 'inline-type-imports' }],
    },
  },
  {
    files: ['*.config.{js,ts}', 'eslint.config.js'],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    files: ['src/shared/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': restrictedImports({
        group: ['@/{app,pages,widgets,features,entities}/**'],
        message: 'Shared cannot depend on business or application layers.',
      }),
    },
  },
  {
    files: ['src/entities/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': restrictedImports({
        group: ['@/{app,pages,widgets,features}/**'],
        message: 'Entities can only depend on shared and explicit entity cross-APIs.',
      }),
    },
  },
  {
    files: ['src/features/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': restrictedImports(
        {
          group: ['@/{app,pages,widgets}/**'],
          message: 'Features cannot depend on widgets, pages, or app.',
        },
        {
          group: ['@/features/*'],
          message: 'Features on the same layer must remain independent.',
        },
      ),
    },
  },
  {
    files: ['src/widgets/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': restrictedImports(
        {
          group: ['@/{app,pages}/**'],
          message: 'Widgets cannot depend on pages or app.',
        },
        {
          group: ['@/widgets/*'],
          message: 'Widgets on the same layer must remain independent.',
        },
      ),
    },
  },
  {
    files: ['src/pages/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': restrictedImports(
        {
          group: ['@/app/**'],
          message: 'Pages cannot depend on app.',
        },
        {
          group: ['@/pages/*'],
          message: 'Pages on the same layer must remain independent.',
        },
      ),
    },
  },
  eslintConfigPrettier,
);
