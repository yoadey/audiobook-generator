import js from '@eslint/js';
import { defineConfig, globalIgnores } from 'eslint/config';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default defineConfig([
  globalIgnores(['node_modules', 'src/api/schema.d.ts']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat['recommended-latest'],
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2023,
      globals: globals.browser,
    },
    rules: {
      // createLink() wraps Mantine components into router links.
      'react-refresh/only-export-components': [
        'error',
        { allowConstantExport: true, extraHOCs: ['createLink'] },
      ],
    },
  },
  {
    files: ['*.{js,cjs}'],
    extends: [js.configs.recommended],
    languageOptions: { globals: globals.node },
  },
]);
