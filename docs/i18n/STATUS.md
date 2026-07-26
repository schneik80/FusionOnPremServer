# Internationalization — status & conventions

The SPA is fully internationalized: every UI/system string renders through
i18next, the language switches live from Settings → Appearance without a
reload, and server errors travel as machine codes the client localizes.
**User-entered data is never translated** — titles, chat messages, wiki
pages, and every other authored string render exactly as typed.

## Locales

`en` (source of truth) plus `de`, `fr`, `es`, `it`, `pt`. The five
non-English catalogs were machine-translation seeded in a formal register
and have **not had a native-speaker review yet** — treat them as beta
quality and expect copy edits. The picker shows each language under its
own native name (`LOCALE_LABEL` in `web/src/i18n/index.ts`); the
preference is user-global (`fdc.locale` in localStorage — deliberately not
per-hub, unlike theme settings).

## Layout & loading

- Catalogs live at `web/src/i18n/locales/<locale>/<namespace>.json`; the
  file name is the namespace. Twelve namespaces: `browse`, `chat`,
  `common`, `details`, `enums`, `errors`, `nav`, `production`, `settings`,
  `tasks`, `whiteboards`, `wiki`.
- English ships eagerly in the entry bundle; every other locale is a lazy
  Vite chunk loaded on first switch (`ensureLocale`). First paint never
  blocks on a catalog fetch.
- Switching goes through `setLocale` in `web/src/state/locale.tsx` (never
  `i18n.changeLanguage` directly): it loads the chunks, re-renders every
  `useTranslation` consumer, and stamps `<html lang>`.

## Conventions (enforced)

- **Semantic keys only** (`settings:port.applyRestart`), never
  English-as-key — copy edits must not silently orphan translations.
- **Enum tokens never render raw.** Status/priority/kind tokens map
  through the helpers in `web/src/i18n/enums.ts` into the `enums`
  namespace.
- **Server errors carry a `code`** in the error envelope
  (`server/respond.go` — `codeForStatus` for category-level statuses,
  `writeErrorCode` for specifics like `hub_not_selected`). The client maps
  codes via `localizeApiError` (`web/src/i18n/apiError.ts`); codes absent
  from the `errors` catalog deliberately fall back to the server's English
  detail message rather than flattening it into a generic sentence.
- **The eslint ratchet** (`npm run lint:i18n`, config in
  `web/eslint.config.js`) fails on literal strings in the extracted
  folders via `i18next/no-literal-string`. New UI code cannot reintroduce
  hardcoded English.
- **Unicode-safe text handling.** Grapheme-aware helpers live in
  `web/src/fmt/graphemes.ts` (Intl.Segmenter with an Array.from
  fallback) — never `.slice()` user text or take `s[0]` for initials; CJK
  and astral-plane input must survive truncation. Pixel-accurate label
  truncation uses `canvas.measureText` (see the Gantt bar renderer),
  because per-character estimates break on double-width glyphs.

## Adding a string

1. Add the key to the English catalog for the right namespace.
2. Add the same key to the five other locales (MT is acceptable seed —
   match the existing formal register).
3. Render it with `useTranslation('<namespace>')`; the ratchet fails the
   lint if you skip extraction.
4. `web/src/i18n/catalogs.test.ts` asserts catalog shape parity across
   locales — a key present in `en` but missing elsewhere fails the suite.

## Known gaps / follow-ups

- Native review of the five MT-seeded catalogs is outstanding.
- Locale coverage is Western-European only; the rendering path (grapheme
  helpers, measured truncation) is already CJK-safe, so adding a CJK
  locale is a catalog task, not an engineering one.
