// Lint config exists for ONE job: the i18n ratchet. `no-literal-string`
// fails on user-facing string literals in JSX so the app can't regress to
// hardcoded English. Every folder's extraction pass has landed, so the
// ratchet now covers all of src/ — a new folder is covered the day it is
// created, not when someone remembers to list it.
//
// react-hooks is registered but not enabled: the codebase carries
// `eslint-disable-next-line react-hooks/exhaustive-deps` comments (written
// for editors/CI that do run that rule), and eslint errors on disable
// directives naming unknown rules.
import tseslint from 'typescript-eslint'
import i18next from 'eslint-plugin-i18next'
import reactHooks from 'eslint-plugin-react-hooks'

const RATCHETED = ['src/**/*.tsx']

export default tseslint.config({
  files: RATCHETED,
  languageOptions: {
    parser: tseslint.parser,
    parserOptions: { ecmaFeatures: { jsx: true } },
  },
  linterOptions: {
    reportUnusedDisableDirectives: 'off',
  },
  plugins: { i18next, 'react-hooks': reactHooks },
  rules: {
    'i18next/no-literal-string': [
      'error',
      {
        mode: 'jsx-only',
        'jsx-attributes': {
          include: ['label', 'title', 'placeholder', 'aria-label', 'alt', 'helperText'],
        },
        callees: { exclude: ['.*'] },
        words: {
          // Non-language glyphs, separators and identifiers that never
          // translate. Entries compile as regexes — escape metachars.
          exclude: [
            '—', '–', '·', '…', '×', '\\+', '%', '/', '›', '»', '\\*', '→', '▼', '▶',
            '99\\+', 'H[1-6]', 'https?://.*',
            'fusionlocalserver', 'T-', 'W',
          ],
        },
      },
    ],
  },
})
