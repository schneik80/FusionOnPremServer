// Lint config exists for ONE job: the i18n ratchet. `no-literal-string`
// fails on user-facing string literals in JSX so extracted folders can't
// regress to hardcoded English. Folders join `RATCHETED` as their
// extraction pass lands; when all of src/ is listed, collapse the globs.
//
// react-hooks is registered but not enabled: the codebase carries
// `eslint-disable-next-line react-hooks/exhaustive-deps` comments (written
// for editors/CI that do run that rule), and eslint errors on disable
// directives naming unknown rules.
import tseslint from 'typescript-eslint'
import i18next from 'eslint-plugin-i18next'
import reactHooks from 'eslint-plugin-react-hooks'

const RATCHETED = [
  'src/components/**/*.tsx',
  'src/tasks/**/*.tsx',
  'src/chat/**/*.tsx',
  'src/wiki/**/*.tsx',
  'src/production/**/*.tsx',
  'src/whiteboards/**/*.tsx',
  'src/embed/**/*.tsx',
]

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
