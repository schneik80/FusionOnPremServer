import { Box } from '@mui/material'
import { useTheme, type Theme } from '@mui/material/styles'
import { basicSetup } from 'codemirror'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tags as tg } from '@lezer/highlight'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { extOf, type ViewerFile } from './kind'
import { gcodeLanguage } from './gcode/gcode'
import { gcodeDialectForExt } from './gcode/registry'
import { FallbackViewer, ViewerSpinner } from './ui'
import { useFileText } from './useFileText'

// TextViewer shows a file's source read-only in CodeMirror (line numbers,
// folding, its own scroller). NC g-code gets dialect-aware highlighting from the
// registry; other text is shown plain. Oversized files fall back to a download.
export function TextViewer({
  file,
  dmProjectId,
  itemId,
}: {
  file: ViewerFile
  dmProjectId: string
  itemId: string
}) {
  const { t } = useTranslation('browse')
  const theme = useTheme()
  const { loading, text, tooLarge, error } = useFileText(dmProjectId, itemId)

  if (loading) return <ViewerSpinner />
  if (error) return <FallbackViewer file={file} reason={error} />
  if (tooLarge) return <FallbackViewer file={file} reason={t('viewer.tooLarge')} />
  return <CodeMirrorReadOnly text={text} ext={extOf(file.name)} theme={theme} />
}

// codeHighlightStyle maps the tags our g-code tokenizer emits to theme colors,
// so highlighting reads correctly in both light and dark mode.
function codeHighlightStyle(theme: Theme) {
  const p = theme.palette
  return HighlightStyle.define([
    { tag: tg.comment, color: p.text.secondary, fontStyle: 'italic' },
    { tag: tg.keyword, color: p.primary.main, fontWeight: '600' }, // G / M / O words
    { tag: tg.controlKeyword, color: p.warning.main, fontWeight: '700' }, // IF / WHILE / GOTO
    { tag: tg.variableName, color: p.info.main }, // axes
    { tag: tg.definition(tg.variableName), color: p.warning.main }, // # macro vars, R/Q params
    { tag: tg.number, color: p.text.primary },
    { tag: tg.atom, color: p.secondary.main }, // feed / speed
    { tag: tg.propertyName, color: p.success.main }, // tool / offsets
    { tag: tg.string, color: p.success.main },
    { tag: tg.meta, color: p.text.disabled }, // line numbers, % marker
    { tag: tg.operator, color: p.text.secondary },
  ])
}

function CodeMirrorReadOnly({ text, ext, theme }: { text: string; ext: string; theme: Theme }) {
  const hostRef = useRef<HTMLDivElement | null>(null)

  const cmTheme = useMemo(
    () =>
      EditorView.theme(
        {
          '&': { height: '100%', backgroundColor: 'transparent', color: theme.palette.text.primary },
          '.cm-scroller': {
            overflow: 'auto',
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            fontSize: '13px',
            lineHeight: '1.6',
          },
          '.cm-gutters': {
            backgroundColor: 'transparent',
            color: theme.palette.text.disabled,
            border: 'none',
          },
          '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'transparent' },
        },
        { dark: theme.palette.mode === 'dark' },
      ),
    [theme],
  )
  const highlight = useMemo(() => syntaxHighlighting(codeHighlightStyle(theme)), [theme])
  const lang = useMemo(() => {
    const dialect = gcodeDialectForExt(ext)
    return dialect ? gcodeLanguage(dialect) : null
  }, [ext])

  useEffect(() => {
    if (!hostRef.current) return
    const view = new EditorView({
      parent: hostRef.current,
      state: EditorState.create({
        doc: text,
        extensions: [
          basicSetup,
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
          EditorView.lineWrapping,
          cmTheme,
          highlight,
          ...(lang ? [lang] : []),
        ],
      }),
    })
    return () => view.destroy()
  }, [text, cmTheme, highlight, lang])

  return (
    <Box
      ref={hostRef}
      sx={{
        height: '72vh',
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        overflow: 'hidden',
        '& .cm-editor': { height: '100%' },
        '& .cm-editor.cm-focused': { outline: 'none' },
      }}
    />
  )
}
