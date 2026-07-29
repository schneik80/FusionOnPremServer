import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faBold,
  faChalkboard,
  faCode,
  faDiagramProject,
  faFileCirclePlus,
  faGlobe,
  faImage,
  faImages,
  faItalic,
  faLink,
  faListCheck,
  faListUl,
  faQuoteRight,
  faSquarePlus,
} from '@fortawesome/free-solid-svg-icons'
import {
  Box,
  Button,
  Chip,
  CircularProgress,
  Divider,
  IconButton,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useTheme } from '@mui/material/styles'
import { basicSetup } from 'codemirror'
import { markdown } from '@codemirror/lang-markdown'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Item } from '../api/types'
import { docRefFromItem, docRefMarkdown } from '../components/doccard/docref'
import { taskRefFromTask, taskRefMarkdown } from '../components/taskcard/taskref'
import { whiteboardRefFromBoard, whiteboardRefMarkdown } from '../components/whiteboardcard/wbref'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
import { AttachTaskDialog } from '../tasks/AttachTaskDialog'
import { QuickTaskDialog } from '../tasks/QuickTaskDialog'
import type { Task } from '../tasks/types'
import { AttachWhiteboardDialog } from '../whiteboards/AttachWhiteboardDialog'
import type { Whiteboard } from '../whiteboards/types'
import {
  HubBrowserDialog,
  hubFileSrc,
  isImageDocument,
  type HubPick,
} from '../components/hubbrowser/HubBrowserDialog'
import type { WikiDraft } from './draftStore'
import { ImageUrlDialog } from './ImageUrlDialog'
import { Markdown } from './Markdown'

interface WikiEditorProps {
  draft: WikiDraft
  markdownValue: string
  titleValue: string
  onChangeMarkdown: (md: string) => void
  onChangeTitle: (title: string) => void
  onDiscard: () => void
  onPublish?: () => void
  publishing?: boolean
  // Uploads an image and resolves to the markdown src + alt to embed. Undefined
  // falls back to inserting an `![alt](url)` placeholder for a manual URL.
  onUploadImage?: (file: File) => Promise<{ src: string; alt: string }>
  // Enables the "embed image from hub" browser: the hub to browse and,
  // optionally, the project to open it at (usually the wiki's own project).
  hubId?: string | null
  hubProject?: Item | null
  saved: boolean // true when the working copy is flushed to IndexedDB
}

// wrapSelection surrounds the current selection with markers (e.g. ** for bold),
// leaving the selection over the wrapped text so a second click toggles cleanly.
function wrapSelection(view: EditorView, before: string, after = before) {
  const { from, to } = view.state.selection.main
  const sel = view.state.sliceDoc(from, to)
  view.dispatch({
    changes: { from, to, insert: before + sel + after },
    selection: { anchor: from + before.length, head: from + before.length + sel.length },
  })
  view.focus()
}

// prefixLine inserts a prefix at the start of the line the cursor sits on
// (headings, list items, blockquotes).
function prefixLine(view: EditorView, prefix: string) {
  const line = view.state.doc.lineAt(view.state.selection.main.from)
  view.dispatch({ changes: { from: line.from, to: line.from, insert: prefix } })
  view.focus()
}

function insertLink(view: EditorView) {
  const { from, to } = view.state.selection.main
  const sel = view.state.sliceDoc(from, to) || 'text'
  const insert = `[${sel}](url)`
  view.dispatch({
    changes: { from, to, insert },
    // select the "url" placeholder so the user can type over it
    selection: { anchor: from + sel.length + 3, head: from + sel.length + 6 },
  })
  view.focus()
}

// insertImage drops an image reference at the cursor. Any selection becomes the
// alt text; the "url" placeholder is left selected to paste/type an image URL
// over. (Uploading an image file into the Wiki folder is a Phase 2 addition,
// riding the same APS upload path as publishing.)
function insertImage(view: EditorView) {
  const { from, to } = view.state.selection.main
  const sel = view.state.sliceDoc(from, to) || 'alt'
  const insert = `![${sel}](url)`
  view.dispatch({
    changes: { from, to, insert },
    // "![" + alt + "](" = sel.length + 4 chars precede the 3-char "url" placeholder
    selection: { anchor: from + sel.length + 4, head: from + sel.length + 7 },
  })
  view.focus()
}

// insertText drops literal text at the cursor, replacing any selection — the
// shared tail of the image-insert and document-card actions.
function insertText(view: EditorView, text: string) {
  const { from, to } = view.state.selection.main
  view.dispatch({ changes: { from, to, insert: text } })
  view.focus()
}

// insertImageRef drops a complete image reference (alt + src both known).
function insertImageRef(view: EditorView, alt: string, src: string) {
  insertText(view, `![${alt}](${src})`)
}

export function WikiEditor({
  draft,
  markdownValue,
  titleValue,
  onChangeMarkdown,
  onChangeTitle,
  onDiscard,
  onPublish,
  publishing,
  onUploadImage,
  hubId,
  hubProject,
  saved,
}: WikiEditorProps) {
  const { t } = useTranslation('wiki')
  const theme = useTheme()
  const hostRef = useRef<HTMLDivElement | null>(null)
  const viewRef = useRef<EditorView | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const [uploadingImage, setUploadingImage] = useState(false)
  const [urlDialogOpen, setUrlDialogOpen] = useState(false)
  const [hubPickerOpen, setHubPickerOpen] = useState(false)
  const [docPickerOpen, setDocPickerOpen] = useState(false)
  const [taskPickerOpen, setTaskPickerOpen] = useState(false)
  const [taskCreateOpen, setTaskCreateOpen] = useState(false)
  const [prodPickerOpen, setProdPickerOpen] = useState(false)
  const [boardPickerOpen, setBoardPickerOpen] = useState(false)
  // Hold the latest onChange in a ref so the update listener (installed once per
  // document) always calls the current callback without re-creating the editor.
  const onChangeRef = useRef(onChangeMarkdown)
  onChangeRef.current = onChangeMarkdown

  // A light MUI-derived CodeMirror theme so the editor blends with the app's
  // light/dark mode instead of CodeMirror's default white.
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
          '.cm-activeLine': { backgroundColor: theme.palette.action.hover },
          '.cm-activeLineGutter': { backgroundColor: theme.palette.action.hover },
          '&.cm-focused .cm-cursor': { borderLeftColor: theme.palette.text.primary },
          '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
            backgroundColor: theme.palette.action.selected,
          },
        },
        { dark: theme.palette.mode === 'dark' },
      ),
    [theme],
  )

  // Markdown token colors. CodeMirror's default highlight style is light-mode
  // oriented — it paints link/url tokens near-black navy, unreadable on the dark
  // slate theme — so derive the readable tags from the MUI palette. This is added
  // after basicSetup (whose default style is a fallback), so it wins for these tags.
  const cmHighlight = useMemo(() => {
    const p = theme.palette
    return syntaxHighlighting(
      HighlightStyle.define([
        // Prose content stays at the full text color and only carries weight /
        // style — so headings, bold, italic, lists and inline code all read
        // crisply; earlier they inherited grey from the list/marker rules.
        { tag: tags.heading, color: p.text.primary, fontWeight: '700' },
        { tag: tags.strong, color: p.text.primary, fontWeight: '700' },
        { tag: tags.emphasis, color: p.text.primary, fontStyle: 'italic' },
        { tag: tags.strikethrough, color: p.text.primary, textDecoration: 'line-through' },
        { tag: tags.list, color: p.text.primary },
        { tag: tags.monospace, color: p.text.primary },
        // Links use the accent.
        { tag: [tags.link, tags.url, tags.labelName], color: p.primary.main, textDecoration: 'underline' },
        // Blockquotes and the syntax punctuation (**, _, -, #, >, `) get one
        // readable muted tone — dim enough to distinguish, not washed out.
        { tag: tags.quote, color: p.text.secondary },
        { tag: [tags.processingInstruction, tags.meta], color: p.text.secondary },
      ]),
    )
  }, [theme])

  // (Re)create the editor when the document changes (switching pages) or the
  // theme flips. Not on every keystroke — the editor owns the live doc.
  useEffect(() => {
    if (!hostRef.current) return
    const view = new EditorView({
      parent: hostRef.current,
      state: EditorState.create({
        doc: markdownValue,
        extensions: [
          basicSetup,
          markdown(),
          EditorView.lineWrapping,
          cmTheme,
          cmHighlight,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current(u.state.doc.toString())
          }),
        ],
      }),
    })
    viewRef.current = view
    return () => {
      view.destroy()
      viewRef.current = null
    }
    // markdownValue is intentionally excluded: seeding once per document avoids
    // clobbering the cursor on each keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.key, cmTheme, cmHighlight])

  const act = (fn: (v: EditorView) => void) => () => {
    if (viewRef.current) fn(viewRef.current)
  }

  // Image button: with an uploader, pick a file, upload it, and insert the
  // resulting reference at the cursor; otherwise fall back to a URL placeholder.
  const onImageClick = onUploadImage
    ? () => fileInputRef.current?.click()
    : act(insertImage)

  async function handleImageFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = '' // allow re-selecting the same file
    if (!file || !onUploadImage || !viewRef.current) return
    setUploadingImage(true)
    try {
      const { src, alt } = await onUploadImage(file)
      insertImageRef(viewRef.current, alt, src)
    } catch {
      // eslint-disable-next-line no-alert
      alert(t('editor.imageUploadFailed'))
    } finally {
      setUploadingImage(false)
    }
  }

  // handleHubPick embeds a document picked in the hub browser as an image
  // reference streaming through the same-origin file endpoint.
  function handleHubPick(pick: HubPick) {
    setHubPickerOpen(false)
    const src = hubFileSrc(pick)
    if (!src || !pick.item || !viewRef.current) return
    const alt = pick.item.name.replace(/\.[^./\\]+$/, '')
    insertImageRef(viewRef.current, alt, src)
  }

  // handleDocPick inserts a document card: a doc-ref link token the markdown
  // renderer unfurls into a DocumentCard (thumbnail + name + location + jump).
  function handleDocPick(pick: HubPick) {
    setDocPickerOpen(false)
    if (!pick.item || !viewRef.current) return
    insertText(viewRef.current, docRefMarkdown(docRefFromItem(pick.hubId, pick.item)))
  }

  // handleTaskPick inserts a task card: the fls:task sibling of the doc-ref
  // token, unfurled into a TaskCard by the same markdown renderer.
  function handleTaskPick(task: Task) {
    setTaskPickerOpen(false)
    setTaskCreateOpen(false)
    if (!viewRef.current) return
    insertText(viewRef.current, taskRefMarkdown(taskRefFromTask(task)))
  }

  // handleBoardPick inserts a whiteboard card — the fls:whiteboard sibling of
  // the task token. The card opens the live board in a dialog, so a published
  // page can point at a sketch without sending the reader hunting for it.
  function handleBoardPick(board: Whiteboard) {
    setBoardPickerOpen(false)
    if (!viewRef.current) return
    insertText(viewRef.current, whiteboardRefMarkdown(whiteboardRefFromBoard(board)))
  }

  const status = saved ? t('editor.savedLocally') : t('editor.saving')

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', flex: 1, minWidth: 0, minHeight: 0 }}>
      {/* Header: title + actions */}
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        sx={{ px: 1.5, py: 1, borderBottom: 1, borderColor: 'divider' }}
      >
        <TextField
          value={titleValue}
          onChange={(e) => onChangeTitle(e.target.value)}
          placeholder={t('editor.titlePlaceholder')}
          variant="standard"
          InputProps={{ disableUnderline: true, sx: { fontSize: 18, fontWeight: 600 } }}
          sx={{ flex: 1, minWidth: 0 }}
        />
        <Typography variant="caption" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>
          {status}
        </Typography>
        <Tooltip title={onPublish ? t('editor.publishTooltip') : t('editor.publishUnavailable')}>
          <span>
            <Button
              size="small"
              variant="contained"
              disabled={!onPublish || publishing}
              onClick={onPublish}
            >
              {publishing ? t('editor.publishing') : t('editor.publish')}
            </Button>
          </span>
        </Tooltip>
        <Button size="small" color="inherit" onClick={onDiscard}>
          {t('common:close')}
        </Button>
      </Stack>

      {/* Formatting toolbar */}
      <Stack
        direction="row"
        spacing={0.25}
        alignItems="center"
        sx={{ px: 1, py: 0.5, borderBottom: 1, borderColor: 'divider', flexWrap: 'wrap' }}
      >
        <ToolBtn title={t('editor.heading1Tooltip')} label="H1" onClick={act((v) => prefixLine(v, '# '))} />
        <ToolBtn title={t('editor.heading2Tooltip')} label="H2" onClick={act((v) => prefixLine(v, '## '))} />
        <ToolBtn title={t('editor.heading3Tooltip')} label="H3" onClick={act((v) => prefixLine(v, '### '))} />
        <Divider orientation="vertical" flexItem sx={{ mx: 0.5 }} />
        <ToolBtn title={t('editor.boldTooltip')} icon={faBold} onClick={act((v) => wrapSelection(v, '**'))} />
        <ToolBtn title={t('editor.italicTooltip')} icon={faItalic} onClick={act((v) => wrapSelection(v, '_'))} />
        <ToolBtn title={t('editor.inlineCodeTooltip')} icon={faCode} onClick={act((v) => wrapSelection(v, '`'))} />
        <Divider orientation="vertical" flexItem sx={{ mx: 0.5 }} />
        <ToolBtn title={t('editor.bulletedListTooltip')} icon={faListUl} onClick={act((v) => prefixLine(v, '- '))} />
        <ToolBtn title={t('editor.quoteTooltip')} icon={faQuoteRight} onClick={act((v) => prefixLine(v, '> '))} />
        <ToolBtn title={t('editor.linkTooltip')} icon={faLink} onClick={act(insertLink)} />
        <ToolBtn
          title={onUploadImage ? t('editor.insertImageUploadTooltip') : t('editor.insertImageUrlTooltip')}
          icon={faImage}
          busy={uploadingImage}
          onClick={onImageClick}
        />
        <ToolBtn
          title={t('editor.insertImageFromPublicUrlTooltip')}
          icon={faGlobe}
          onClick={() => setUrlDialogOpen(true)}
        />
        {hubId && (
          <ToolBtn
            title={t('editor.embedHubImageTooltip')}
            icon={faImages}
            onClick={() => setHubPickerOpen(true)}
          />
        )}
        {hubId && (
          <ToolBtn
            title={t('editor.insertDocCardTooltip')}
            icon={faFileCirclePlus}
            onClick={() => setDocPickerOpen(true)}
          />
        )}
        {hubProject && (
          <ToolBtn
            title={t('editor.insertTaskCardTooltip')}
            icon={faListCheck}
            onClick={() => setTaskPickerOpen(true)}
          />
        )}
        {hubProject && (
          <ToolBtn
            title={t('editor.createTaskCardTooltip')}
            icon={faSquarePlus}
            onClick={() => setTaskCreateOpen(true)}
          />
        )}
        {hubProject && (
          <ToolBtn
            title={t('editor.insertProdCardTooltip')}
            icon={faDiagramProject}
            onClick={() => setProdPickerOpen(true)}
          />
        )}
        {hubProject && (
          <ToolBtn
            title={t('editor.insertBoardCardTooltip')}
            icon={faChalkboard}
            onClick={() => setBoardPickerOpen(true)}
          />
        )}
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          hidden
          onChange={handleImageFile}
        />
      </Stack>

      {/* Split pane: source (left) + live preview (right) */}
      <Box sx={{ display: 'flex', flex: 1, minHeight: 0 }}>
        <Box
          ref={hostRef}
          sx={{
            flex: 1,
            minWidth: 0,
            borderRight: 1,
            borderColor: 'divider',
            overflow: 'hidden',
            '& .cm-editor': { height: '100%' },
            '& .cm-editor.cm-focused': { outline: 'none' },
          }}
        />
        <Box sx={{ flex: 1, minWidth: 0, overflowY: 'auto', p: 2 }}>
          <PreviewHeader status={draft.status} />
          <Markdown>{markdownValue || `_${t('editor.emptyPreview')}_`}</Markdown>
        </Box>
      </Box>

      <ImageUrlDialog
        open={urlDialogOpen}
        onClose={() => setUrlDialogOpen(false)}
        onInsert={(url, alt) => {
          setUrlDialogOpen(false)
          if (viewRef.current) insertImageRef(viewRef.current, alt, url)
        }}
      />
      {hubId && (
        <HubBrowserDialog
          open={hubPickerOpen}
          hubId={hubId}
          title={t('editor.hubImageDialogTitle')}
          selectable={isImageDocument}
          initialProject={hubProject}
          pickLabel={t('editor.embedImage')}
          onClose={() => setHubPickerOpen(false)}
          onPick={handleHubPick}
        />
      )}
      {hubId && (
        <HubBrowserDialog
          open={docPickerOpen}
          hubId={hubId}
          title={t('editor.docCardDialogTitle')}
          initialProject={hubProject}
          pickLabel={t('editor.insertCard')}
          onClose={() => setDocPickerOpen(false)}
          onPick={handleDocPick}
        />
      )}
      {hubProject && taskPickerOpen && (
        <AttachTaskDialog
          open={taskPickerOpen}
          projectId={hubProject.id}
          onClose={() => setTaskPickerOpen(false)}
          onPick={handleTaskPick}
        />
      )}
      {hubProject && taskCreateOpen && (
        <QuickTaskDialog
          open={taskCreateOpen}
          onClose={() => setTaskCreateOpen(false)}
          projectId={hubProject.id}
          hubId={hubId ?? ''}
          projectName={hubProject.name}
          onCreated={handleTaskPick}
        />
      )}
      {hubProject && prodPickerOpen && (
        <ProductionRefDialog
          open={prodPickerOpen}
          projectId={hubProject.id}
          hubId={hubId ?? ''}
          projectName={hubProject.name}
          onClose={() => setProdPickerOpen(false)}
          onPick={(token, label) => {
            if (viewRef.current) insertText(viewRef.current, `[${label.replace(/[[\]]/g, '')}](${token})`)
          }}
        />
      )}
      {hubProject && boardPickerOpen && (
        <AttachWhiteboardDialog
          open={boardPickerOpen}
          projectId={hubProject.id}
          onClose={() => setBoardPickerOpen(false)}
          onPick={handleBoardPick}
        />
      )}
    </Box>
  )
}

function ToolBtn({
  title,
  icon,
  label,
  busy,
  onClick,
}: {
  title: string
  icon?: typeof faBold
  label?: string
  busy?: boolean
  onClick: () => void
}) {
  return (
    <Tooltip title={title}>
      <IconButton
        size="small"
        disabled={busy}
        onClick={onClick}
        sx={{ color: 'text.secondary', width: 30, height: 30 }}
      >
        {busy ? (
          <CircularProgress size={13} />
        ) : icon ? (
          <FontAwesomeIcon icon={icon} style={{ fontSize: 13 }} />
        ) : (
          <Typography variant="caption" sx={{ fontWeight: 700, fontSize: 12 }}>
            {label}
          </Typography>
        )}
      </IconButton>
    </Tooltip>
  )
}

function PreviewHeader({ status }: { status: WikiDraft['status'] }) {
  const { t } = useTranslation('wiki')
  return (
    <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 1 }}>
      <Typography variant="overline" color="text.secondary">
        {t('editor.preview')}
      </Typography>
      <Chip
        size="small"
        label={
          status === 'draft'
            ? t('status.localDraft')
            : status === 'modified'
              ? t('status.unpublishedChanges')
              : t('status.published')
        }
        color={status === 'published' ? 'success' : 'default'}
        variant="outlined"
        sx={{ height: 18, fontSize: 10 }}
      />
    </Stack>
  )
}
