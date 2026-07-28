import { createTheme, type Theme } from '@mui/material/styles'

// Palette tokens lifted from the sibling PowerTools-Assembly project so the web
// UI matches Fusion's own light/dark chrome.
// (commands/assemblybuilder/resources/html/index.html)
const accent = '#0696d7'

// The rust-orange second accent. It marks the things that are deliberately set
// apart from the primary accent: production runs (vs prove-out) and the History
// graph's public-share lane. It rides MUI's `secondary` slot — nothing else in
// the app used that slot — so components read it as theme.palette.secondary.main
// rather than repeating the literal, and Settings → Appearance can retheme it.
const accentAlt = '#b7410e'

// The amber third accent. It marks "pay attention to this" without meaning
// failure: an as-run artifact on a batch card, a high-priority task, a blocked
// Gantt bar, a backup warning. Those all read MUI's `warning` slot, which until
// now was MUI's own stock orange — outside our palette and unreachable from
// Settings, which is exactly why the batch cards' yellow couldn't be themed.
const accentWarn = '#ffab00'

const dark = {
  bgPrimary: '#2A3442',
  bgPanel: '#323E50',
  bgHover: '#242E39',
  border: '#4a5568',
  textPrimary: '#ffffff',
  textSecondary: '#a0aec0',
  textMuted: '#718096',
  accentHover: '#0aa3e8',
}

const light = {
  bgPrimary: '#f4f4f4',
  bgPanel: '#ffffff',
  bgHover: '#e8e8e8',
  border: '#d1d5db',
  textPrimary: '#333333',
  textSecondary: '#555555',
  textMuted: '#888888',
  accentHover: '#0580b8',
}

export type ColorMode = 'light' | 'dark'

// ThemeTokens is the shape of one mode's token bag; ThemeOverride is a user's
// partial customization of it (Settings → Appearance → Custom colors), which
// may also replace either accent color.
export type ThemeTokens = typeof dark
export type ThemeOverride = Partial<ThemeTokens> & {
  accent?: string
  accentAlt?: string
  accentWarn?: string
}

// Montserrat carries no CJK/Arabic glyphs; system-ui and the Noto/Segoe
// entries let the OS supply coverage for scripts we don't bundle, instead
// of falling through to an unmanaged default.
const fontFamily =
  '"Montserrat", system-ui, "Segoe UI", "Noto Sans", "Helvetica Neue", Arial, sans-serif'

export function makeTheme(mode: ColorMode, overrides?: ThemeOverride): Theme {
  const {
    accent: accentOverride,
    accentAlt: accentAltOverride,
    accentWarn: accentWarnOverride,
    ...tokenOverrides
  } = overrides ?? {}
  const t = { ...(mode === 'dark' ? dark : light), ...tokenOverrides }
  const ac = accentOverride ?? accent
  const acAlt = accentAltOverride ?? accentAlt
  const acWarn = accentWarnOverride ?? accentWarn
  return createTheme({
    palette: {
      mode,
      primary: { main: ac, dark: t.accentHover, contrastText: '#ffffff' },
      secondary: { main: acAlt, contrastText: '#ffffff' },
      // Amber needs dark ink, unlike the two accents.
      warning: { main: acWarn, contrastText: '#1a1a1a' },
      background: { default: t.bgPrimary, paper: t.bgPanel },
      text: { primary: t.textPrimary, secondary: t.textSecondary },
      divider: t.border,
    },
    typography: {
      fontFamily,
      fontSize: 13,
      h6: { fontWeight: 600 },
      subtitle2: { fontWeight: 600 },
    },
    shape: { borderRadius: 6 },
    components: {
      MuiCssBaseline: {
        styleOverrides: {
          // Theme the scrollbars so they match the palette instead of the OS
          // default (desktop Chrome renders chunky light-grey bars otherwise).
          // Firefox and Chromium 121+ honor the standard properties; the
          // ::-webkit-* rules cover older/desktop Chrome and Safari.
          '*': {
            scrollbarColor: `${t.border} transparent`,
            scrollbarWidth: 'thin',
          },
          '*::-webkit-scrollbar': { width: 10, height: 10 },
          '*::-webkit-scrollbar-track': { backgroundColor: 'transparent' },
          '*::-webkit-scrollbar-thumb': {
            backgroundColor: t.border,
            borderRadius: 8,
          },
          '*::-webkit-scrollbar-thumb:hover': { backgroundColor: t.textMuted },
          '*::-webkit-scrollbar-corner': { backgroundColor: 'transparent' },
        },
      },
      MuiAppBar: {
        styleOverrides: {
          colorPrimary: { backgroundColor: t.bgPanel, color: t.textPrimary },
        },
        defaultProps: { elevation: 0 },
      },
      MuiDrawer: {
        styleOverrides: {
          paper: { backgroundColor: t.bgPanel, borderColor: t.border },
        },
      },
      MuiListItemButton: {
        styleOverrides: {
          root: {
            '&:hover': { backgroundColor: t.bgHover },
            '&.Mui-selected': {
              backgroundColor: t.bgHover,
              borderLeft: `3px solid ${ac}`,
            },
            '&.Mui-selected:hover': { backgroundColor: t.bgHover },
          },
        },
      },
      MuiTooltip: {
        defaultProps: { arrow: true },
      },
    },
  })
}

// Custom token bag exposed to components that need raw palette values beyond
// MUI's semantic slots (e.g. muted text for type tags).
export const tokens = { dark, light }

// The stock accents, exported so the Appearance tool can show them as the
// default swatch values beside the mode token bags.
export const defaultAccent = accent
export const defaultAccentAlt = accentAlt
export const defaultAccentWarn = accentWarn
