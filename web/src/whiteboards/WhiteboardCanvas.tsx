import { faDiagramProject, faListCheck, faPaperclip, faSitemap } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Alert, Box, Button, CircularProgress, Snackbar, Stack, Tooltip, Typography } from '@mui/material'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  FrameShapeUtil,
  Tldraw,
  createBindingId,
  createShapeId,
  createTLStore,
  defaultBindingUtils,
  defaultShapeUtils,
  getSnapshot,
  loadSnapshot,
  type Editor,
  type TLShapeId,
} from 'tldraw'
import 'tldraw/tldraw.css'
import { getAssetUrlsByMetaUrl } from '@tldraw/assets/urls'
import { ApiError, api } from '../api/client'
import { useAuthMe } from '../api/queries'
import { firstGrapheme } from '../fmt/graphemes'
import { useColorMode } from '../state/colorMode'
import { useNav } from '../state/nav'
import { docRefFromItem, encodeDocRef } from '../components/doccard/docref'
import { encodeTaskRef, taskRefFromTask } from '../components/taskcard/taskref'
import { HubBrowserDialog, type HubPick } from '../components/hubbrowser/HubBrowserDialog'
import { AttachTaskDialog } from '../tasks/AttachTaskDialog'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
import { CARD_H, CARD_W, FLS_CARD_TYPE, FlsCardShapeUtil } from './cardshape'
import { useBoardEvents, type BoardPeer } from './useBoardEvents'
import './whiteboard.css'

// tldraw loads its fonts, icons and translations from cdn.tldraw.com by
// default. This app ships a strict CSP (default-src 'self'), so every one of
// those requests was blocked: the fonts never arrived (unreadable canvas text)
// and the blocked translations fetch rejected inside React's commit phase,
// which is what killed the board a few seconds after it opened.
//
// Resolving the assets through the bundler instead makes Vite emit them as
// same-origin files, so nothing is fetched cross-origin, the CSP stays strict,
// and the whiteboard works offline like the rest of this local-first app.
// Built once at module scope: tldraw requires this object be stable.
const ASSET_URLS = getAssetUrlsByMetaUrl()

// tldraw's SDK licence key, inlined at build time from web/.env.local (see
// web/.env.example). Without it tldraw treats any HTTPS page on a non-loopback
// hostname as an unlicensed production deployment and, five seconds after the
// editor mounts, swaps it for a `display: none` div — no error, no exception,
// the board simply vanishes. This app is served over HTTPS on a LAN hostname,
// so that is exactly what happened before the key was supplied.
const LICENSE_KEY = import.meta.env.VITE_TLDRAW_LICENSE_KEY

// Frames ship with NO styling controls: tldraw declares their colour as a plain
// validator rather than a style prop, "so they don't get picked up by the editor
// as a style prop by default" (@tldraw/tlschema TLFrameShape) — which is why
// selecting a frame showed an empty style panel. configure({showColors:true})
// swaps that validator for the real DefaultColorStyle, so the panel offers a
// colour and the frame's border and heading paint with it. Colour is the whole
// of what tldraw 5 supports for frames; their props are w/h/name/color.
//
// Built once at module scope, like ASSET_URLS: the store schema and the editor
// must be given the SAME util, and a new instance per render would rebuild the
// schema underneath a live document.
const FRAME_UTIL = FrameShapeUtil.configure({ showColors: true })

// The store's shape utils, frame swapped for the configured one. It has to be a
// swap and not an append: createTLStore throws "Shape type frame is defined more
// than once" if both reach it.
const STORE_SHAPE_UTILS = [
  ...defaultShapeUtils.filter((u) => u.type !== 'frame'),
  FRAME_UTIL,
  FlsCardShapeUtil,
]

// tldraw ships no plain 'pt' — only pt-pt and pt-br — and our catalogues are
// European Portuguese, so that one needs mapping. Everything else this app
// speaks, tldraw speaks under the same code.
const TLDRAW_LOCALE: Record<string, string> = { pt: 'pt-pt' }
const tldrawLocale = (appLocale: string) => TLDRAW_LOCALE[appLocale] ?? appLocale

// How long the canvas sits idle before persisting. Long enough that a stroke or
// a drag isn't a request each, short enough that a closed tab loses little.
const SAVE_DEBOUNCE_MS = 1500

type Pending = 'task' | 'production' | 'document' | 'assembly' | null

// WhiteboardCanvas hosts one tldraw board: it loads the stored document once,
// autosaves the document scope on a debounce, and adds the app's own card
// shapes to the canvas. The tldraw UI is skinned to the app in whiteboard.css.
//
// The document is owned here rather than by react-query: it is large, it is
// written far more often than it is read, and the editor is its source of
// truth while a board is open — a polling cache would fight the user's strokes.
export function WhiteboardCanvas({
  projectId,
  boardId,
  canWrite,
}: {
  projectId: string
  boardId: string
  canWrite: boolean
}) {
  const { t, i18n } = useTranslation('whiteboards')
  const nav = useNav()
  const me = useAuthMe().data?.user
  const { mode } = useColorMode()
  const locale = tldrawLocale(i18n.language)

  // tldraw's own style vocabulary, relabelled. Its "Dash" row offers draw,
  // solid, dashed, dotted — three of which aren't dashes — and its fills are
  // named Semi / Pattern / Fill / Lined fill, which say nothing about what they
  // look like. Overriding the strings renames the row titles, the tooltips and
  // the a11y labels together, without removing any option.
  //
  // Only the active locale is built: catalogs load lazily (see i18n/index.ts),
  // so the other five aren't in memory, and tldraw looks up nothing else. The
  // provider re-applies overrides whenever this object or the locale changes,
  // so a language switch swaps both halves at once — hence the memo, without
  // which every render would re-run that effect.
  const tldrawOverrides = useMemo(
    () => ({
      translations: {
        [locale]: {
          'style-panel.dash': t('tldraw.strokeLabel'),
          'dash-style.draw': t('tldraw.strokeSketch'),
          'fill-style.semi': t('tldraw.fillTinted'),
          'fill-style.pattern': t('tldraw.fillHatched'),
          'fill-style.fill': t('tldraw.fillFilled'),
          'fill-style.lined-fill': t('tldraw.fillLined'),
        },
      },
    }),
    [t, locale],
  )
  // The store's schema must know EVERY shape the editor can create, not just
  // ours: building it from the custom util alone leaves out draw/geo/arrow/text
  // and the binding utils, so the default tools write records the schema
  // rejects — which surfaces as the whole editor failing to mount.
  const [store] = useState(() =>
    createTLStore({
      shapeUtils: STORE_SHAPE_UTILS,
      bindingUtils: [...defaultBindingUtils],
    }),
  )
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [pending, setPending] = useState<Pending>(null)
  // The assembly flow makes two APS calls (details → occurrences), so it shows a
  // busy state on its button; `notice` surfaces the after-the-fact summary
  // (children skipped for having no design, or a plain part with none at all).
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  // conflict holds the stale-save state: someone else saved this board while it
  // was open. Autosaving stops until the user picks reload or overwrite —
  // retrying is exactly what would discard their work.
  const [conflict, setConflict] = useState(false)
  // Who saved over us, for the banner. Empty when the conflict surfaced from a
  // refused save rather than from the awareness stream.
  const [conflictBy, setConflictBy] = useState('')

  const editorRef = useRef<Editor | null>(null)
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Suppresses the change burst that loading a document naturally produces —
  // without it, opening a board would immediately save it straight back.
  const hydrated = useRef(false)
  // The revision this canvas is working from: what it loaded, then what each
  // save returned. Sent with every save so the server can refuse one built on a
  // revision the board has already moved past. A ref, not state: the autosave
  // closure must read the current value, and a re-render per save would be
  // noise.
  const docRev = useRef(0)
  // Set once the user chooses "overwrite" — the next save (and only the next)
  // goes through unconditionally.
  const forceNext = useRef(false)
  // Mirrors `conflict` for the save closures: the unmount flush runs from a
  // cleanup whose closure predates the conflict, and it must not fire a save
  // the user hasn't resolved.
  const conflictRef = useRef(false)

  // ---- load once per board ----
  useEffect(() => {
    let cancelled = false
    hydrated.current = false
    setLoading(true)
    setLoadError(null)
    conflictRef.current = false
    setConflict(false)
    api
      .whiteboardDoc(projectId, boardId)
      .then(({ doc, rev }) => {
        if (cancelled) return
        docRev.current = rev
        // We store only the document scope, so restore it as such — session
        // state (camera, selection) stays per-user and is never persisted.
        if (doc) loadSnapshot(store, { document: doc as never })
      })
      .catch((e) => {
        if (!cancelled) setLoadError(e instanceof Error ? e.message : t('canvas.loadFailed'))
      })
      .finally(() => {
        if (cancelled) return
        setLoading(false)
        hydrated.current = true
      })
    return () => {
      cancelled = true
    }
  }, [projectId, boardId, store])

  const flush = useCallback(async () => {
    if (!hydrated.current || !canWrite) return
    const force = forceNext.current
    if (conflictRef.current && !force) return // waiting on the user's choice
    const { document } = getSnapshot(store)
    setSaving(true)
    try {
      const saved = await api.whiteboardDocSave(projectId, boardId, document, docRev.current, force)
      docRev.current = saved.docRev
      forceNext.current = false
      conflictRef.current = false
      setConflict(false)
    } catch (e) {
      // A stale save is the one failure that must NOT be shrugged off: someone
      // else has saved this board, and the next autosave tick would overwrite
      // them. Stop saving and hand the choice to the user.
      if (e instanceof ApiError && e.code === 'whiteboard_stale') {
        forceNext.current = false
        conflictRef.current = true
        setConflict(true)
        return
      }
      // Everything else stays best-effort: the next change reschedules, and the
      // editor still holds the user's work. A toast on every blip would be
      // noisier than useful.
    } finally {
      setSaving(false)
    }
  }, [store, projectId, boardId, canWrite])

  // ---- autosave the document scope ----
  useEffect(() => {
    if (!canWrite || conflict) return
    const unlisten = store.listen(
      () => {
        if (!hydrated.current) return
        if (saveTimer.current) clearTimeout(saveTimer.current)
        saveTimer.current = setTimeout(() => void flush(), SAVE_DEBOUNCE_MS)
      },
      // Only real document edits: 'session' covers camera/selection, which are
      // per-user view state and must not dirty a shared board.
      { scope: 'document', source: 'user' },
    )
    return () => {
      unlisten()
      if (saveTimer.current) clearTimeout(saveTimer.current)
    }
  }, [store, flush, canWrite, conflict])

  // Persist on unmount (switching boards, leaving the tab) so the last edits
  // inside the debounce window aren't lost.
  useEffect(() => {
    return () => {
      if (saveTimer.current) {
        clearTimeout(saveTimer.current)
        void flush()
      }
    }
  }, [flush])

  // Follow the app's light/dark mode and language rather than tldraw's own
  // defaults — left to itself it takes the colour scheme from the OS and the
  // language from the browser, so the canvas could sit in German inside an
  // English app.
  useEffect(() => {
    editorRef.current?.user.updateUserPreferences({ colorScheme: mode, locale })
  }, [mode, locale])

  // ---- awareness ----

  // Someone else saved. If this canvas is behind, stop autosaving now and say
  // so, rather than letting the next debounce tick discover it as a refusal —
  // same choice offered either way, just offered sooner.
  //
  // A save of our own arrives here too (the server broadcasts to the whole
  // room, sender included); by then flush() has already advanced docRev to
  // that revision, so the comparison quietly ignores it.
  const onRemoteSave = useCallback((rev: number, by?: BoardPeer) => {
    if (rev <= docRev.current) return
    conflictRef.current = true
    setConflict(true)
    setConflictBy(by?.name ?? '')
  }, [])

  const { peers } = useBoardEvents(projectId, boardId, onRemoteSave)
  // Everyone on the board except this session's own user — the header shows
  // "who else", and seeing yourself in it is just noise.
  const others = peers.filter((p) => !me?.id || p.userId !== me.id)

  // ---- resolving a conflict ----

  // Take the server's version: re-fetch the document and its revision, and
  // replace what is on the canvas. This canvas's unsaved edits are lost, which
  // is why it is a deliberate click and not something the app does on its own.
  const reloadFromServer = useCallback(async () => {
    try {
      const { doc, rev } = await api.whiteboardDoc(projectId, boardId)
      hydrated.current = false
      docRev.current = rev
      loadSnapshot(store, { document: (doc ?? { store: {}, schema: {} }) as never })
      conflictRef.current = false
      setConflict(false)
    } catch (e) {
      setNotice(e instanceof Error ? e.message : t('canvas.loadFailed'))
    } finally {
      hydrated.current = true
    }
  }, [projectId, boardId, store, t])

  // Keep this canvas and overwrite what the server holds. The other save is
  // discarded — acknowledged, not silent, which is the whole difference from
  // the behaviour this replaces.
  const overwriteServer = useCallback(() => {
    forceNext.current = true
    void flush()
  }, [flush])

  // Drop a card at the centre of the current viewport.
  const placeCard = (token: string) => {
    const editor = editorRef.current
    if (!editor || !token) return
    const { x, y } = editor.getViewportPageBounds().center
    editor.createShape({
      type: FLS_CARD_TYPE,
      x: x - CARD_W / 2,
      y: y - CARD_H / 2,
      props: { w: CARD_W, h: CARD_H, token },
    })
  }

  // Drop an assembly and its children as a tree: the root card on top, a card
  // per child laid out in centered rows beneath, each joined to the root by a
  // bound arrow (bound, so the arrow follows when the user rearranges a card).
  const placeAssembly = (rootToken: string, childTokens: string[]) => {
    const editor = editorRef.current
    if (!editor || !rootToken) return

    const COL_GAP = 40 // horizontal gap between sibling cards
    const ROW_GAP = 120 // vertical gap between the root row and each child row
    const MAX_COLS = 4 // wrap wide fans to rows so a big assembly stays readable

    const { x: cx, y: cy } = editor.getViewportPageBounds().center
    const rows = Math.max(1, Math.ceil(childTokens.length / MAX_COLS))
    // Centre the whole cluster on the viewport: root row + child rows.
    const totalH = CARD_H + (childTokens.length ? ROW_GAP + rows * CARD_H + (rows - 1) * ROW_GAP : 0)
    const rootY = cy - totalH / 2
    const rootX = cx - CARD_W / 2

    const rootId = createShapeId()
    const shapes: {
      id: TLShapeId
      type: typeof FLS_CARD_TYPE
      x: number
      y: number
      props: { w: number; h: number; token: string }
    }[] = [{ id: rootId, type: FLS_CARD_TYPE, x: rootX, y: rootY, props: { w: CARD_W, h: CARD_H, token: rootToken } }]

    const childIds: TLShapeId[] = []
    childTokens.forEach((token, i) => {
      const rowIdx = Math.floor(i / MAX_COLS)
      const colInRow = i % MAX_COLS
      const countInRow = Math.min(MAX_COLS, childTokens.length - rowIdx * MAX_COLS)
      const rowWidth = countInRow * CARD_W + (countInRow - 1) * COL_GAP
      const startX = cx - rowWidth / 2
      const id = createShapeId()
      childIds.push(id)
      shapes.push({
        id,
        type: FLS_CARD_TYPE,
        x: startX + colInRow * (CARD_W + COL_GAP),
        y: rootY + CARD_H + ROW_GAP + rowIdx * (CARD_H + ROW_GAP),
        props: { w: CARD_W, h: CARD_H, token },
      })
    })

    // One undo step for the whole tree, and leave it selected like a paste.
    editor.run(() => {
      editor.createShapes(shapes)
      if (childIds.length === 0) {
        editor.select(rootId)
        return
      }
      const arrows = childIds.map((childId) => {
        const arrowId = createShapeId()
        const child = shapes.find((s) => s.id === childId)!
        return {
          arrowId,
          childId,
          // Local coords (shape at 0,0) so these read as page space; the
          // bindings below refine the exact terminals and keep them attached.
          start: { x: cx, y: rootY + CARD_H },
          end: { x: child.x + CARD_W / 2, y: child.y },
        }
      })
      editor.createShapes(
        arrows.map((a) => ({
          id: a.arrowId,
          type: 'arrow' as const,
          x: 0,
          y: 0,
          props: { start: a.start, end: a.end },
        })),
      )
      editor.createBindings(
        arrows.flatMap((a) => [
          {
            id: createBindingId(),
            type: 'arrow' as const,
            fromId: a.arrowId,
            toId: rootId,
            props: { terminal: 'start' as const, normalizedAnchor: { x: 0.5, y: 1 }, isExact: false, isPrecise: true },
          },
          {
            id: createBindingId(),
            type: 'arrow' as const,
            fromId: a.arrowId,
            toId: a.childId,
            props: { terminal: 'end' as const, normalizedAnchor: { x: 0.5, y: 0 }, isExact: false, isPrecise: true },
          },
        ]),
      )
      editor.select(rootId, ...childIds)
    })
  }

  // Resolve the picked assembly to a card tree: get its root component version
  // (the DM hub-browser listing doesn't carry one), fetch its immediate
  // occurrences, and turn each child with an owning design into an fls:doc card.
  // Two cached one-shot calls — deliberately NOT a recursive walk, which would
  // fan out an occurrences call per node against the per-minute quota.
  const addAssembly = async (pick: HubPick) => {
    if (!pick.item) return
    const hubId = pick.hubId
    const item = pick.item
    setBusy(true)
    try {
      let cvId = item.componentVersionId
      if (!cvId) {
        const details = await api.itemDetails(hubId, item.id)
        cvId = details.rootComponentVersionId
      }
      if (!cvId) {
        setNotice(t('assembly.noComponentVersion', { name: item.name }))
        return
      }
      const children = await api.uses({ cvId, hubId })

      // One card per distinct child design. Occurrences without an owning design
      // (in-context bodies never saved as their own document) can't become
      // fls:doc cards, so they're skipped — and counted, never dropped silently.
      const seen = new Set<string>()
      const childTokens: string[] = []
      let skipped = 0
      for (const c of children) {
        if (!c.designItemId) {
          skipped++
          continue
        }
        if (seen.has(c.designItemId)) continue
        seen.add(c.designItemId)
        childTokens.push(
          encodeDocRef({ hubId, itemId: c.designItemId, name: c.designItemName || c.name, kind: 'design' }),
        )
      }

      placeAssembly(encodeDocRef(docRefFromItem(hubId, item)), childTokens)

      if (childTokens.length === 0) {
        setNotice(t('assembly.noChildren', { name: item.name }))
      } else if (skipped > 0) {
        setNotice(t('assembly.placedSkipped', { count: childTokens.length, skipped }))
      }
    } catch (e) {
      setNotice(e instanceof Error ? e.message : t('assembly.expandFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
      {canWrite && (
        <Stack
          direction="row"
          spacing={1}
          alignItems="center"
          sx={{ px: 1.5, py: 0.75, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
        >
          <Typography variant="caption" color="text.secondary">
            {t('canvas.placeCard')}
          </Typography>
          <Button
            size="small"
            startIcon={<FontAwesomeIcon icon={faListCheck} style={{ fontSize: 11 }} />}
            onClick={() => setPending('task')}
            sx={{ textTransform: 'none' }}
          >
            {t('canvas.taskButton')}
          </Button>
          <Button
            size="small"
            startIcon={<FontAwesomeIcon icon={faDiagramProject} style={{ fontSize: 11 }} />}
            onClick={() => setPending('production')}
            sx={{ textTransform: 'none' }}
          >
            {t('canvas.jobBatchButton')}
          </Button>
          <Button
            size="small"
            startIcon={<FontAwesomeIcon icon={faPaperclip} style={{ fontSize: 11 }} />}
            onClick={() => setPending('document')}
            sx={{ textTransform: 'none' }}
          >
            {t('canvas.documentButton')}
          </Button>
          <Button
            size="small"
            disabled={busy}
            startIcon={
              busy ? (
                <CircularProgress size={11} />
              ) : (
                <FontAwesomeIcon icon={faSitemap} style={{ fontSize: 11 }} />
              )
            }
            onClick={() => setPending('assembly')}
            sx={{ textTransform: 'none' }}
          >
            {t('canvas.assemblyButton')}
          </Button>
          <Box sx={{ flex: 1 }} />
          <BoardPeers peers={others} />
          <Typography variant="caption" color="text.disabled" sx={{ transition: 'opacity .2s' }}>
            {conflict ? t('canvas.notSaving') : saving ? t('canvas.saving') : t('canvas.saved')}
          </Typography>
        </Stack>
      )}

      {/* Someone else saved this board while it was open. Autosaving has
          stopped: the two choices are theirs or yours, and the app must not
          pick for them. Reload discards this canvas's unsaved edits; overwrite
          discards the other save. Nothing happens until they choose. */}
      {conflict && (
        <Alert
          severity="warning"
          sx={{ borderRadius: 0, py: 0.25 }}
          action={
            <Stack direction="row" spacing={1}>
              <Button size="small" color="inherit" onClick={reloadFromServer}>
                {t('canvas.conflictReload')}
              </Button>
              <Button size="small" color="inherit" onClick={overwriteServer}>
                {t('canvas.conflictOverwrite')}
              </Button>
            </Stack>
          }
        >
          {conflictBy ? t('canvas.conflictBy', { name: conflictBy }) : t('canvas.conflict')}
        </Alert>
      )}

      {/* data-fls-drop-owner: the canvas handles its own drops (tldraw turns a
          dropped file into a shape and stops the event propagating), so the
          app-wide upload drop handler in state/uploads.tsx stays out of it —
          otherwise a drop here also opened the upload dialog, and the drop it
          never saw left the upload overlay stuck over the whole window. */}
      <Box
        sx={{ flex: 1, minHeight: 0, position: 'relative' }}
        className="fls-tldraw"
        data-fls-drop-owner=""
      >
        {loading && (
          <Box sx={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', zIndex: 2 }}>
            <CircularProgress size={22} />
          </Box>
        )}
        {loadError ? (
          <Box sx={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', px: 3 }}>
            <Typography variant="body2" color="error">
              {loadError}
            </Typography>
          </Box>
        ) : (
          <Tldraw
            store={store}
            licenseKey={LICENSE_KEY}
            assetUrls={ASSET_URLS}
            // Appending is right here (unlike the store's list): Tldraw merges
            // with mergeArraysAndReplaceDefaults by type, so FRAME_UTIL takes
            // the default frame util's place rather than colliding with it.
            shapeUtils={[FlsCardShapeUtil, FRAME_UTIL]}
            overrides={tldrawOverrides}
            onMount={(editor) => {
              editorRef.current = editor
              editor.user.updateUserPreferences({ colorScheme: mode, locale })
              editor.updateInstanceState({ isReadonly: !canWrite })
            }}
          />
        )}
      </Box>

      {pending === 'task' && (
        <AttachTaskDialog
          open
          projectId={projectId}
          onClose={() => setPending(null)}
          onPick={(task) => {
            setPending(null)
            placeCard(encodeTaskRef(taskRefFromTask(task)))
          }}
        />
      )}
      {pending === 'production' && (
        <ProductionRefDialog
          open
          projectId={projectId}
          hubId={nav.hubId ?? ''}
          projectName={nav.project?.name ?? ''}
          onClose={() => setPending(null)}
          onPick={(token) => placeCard(token)}
        />
      )}
      {pending === 'document' && (
        <HubBrowserDialog
          open
          hubId={nav.hubId ?? null}
          title={t('canvas.placeDocumentTitle')}
          pickLabel={t('canvas.place')}
          initialProject={nav.project ?? null}
          onClose={() => setPending(null)}
          onPick={(pick) => {
            setPending(null)
            if (!pick.item) return
            placeCard(encodeDocRef(docRefFromItem(pick.hubId, pick.item)))
          }}
        />
      )}
      {pending === 'assembly' && (
        <HubBrowserDialog
          open
          hubId={nav.hubId ?? null}
          title={t('assembly.expandTitle')}
          pickLabel={t('assembly.expand')}
          initialProject={nav.project ?? null}
          onClose={() => setPending(null)}
          onPick={(pick) => {
            setPending(null)
            void addAssembly(pick)
          }}
        />
      )}
      <Snackbar
        open={!!notice}
        autoHideDuration={5000}
        onClose={() => setNotice(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        <Alert severity="info" variant="filled" onClose={() => setNotice(null)} sx={{ fontSize: 13 }}>
          {notice}
        </Alert>
      </Snackbar>
    </Box>
  )
}

// BoardPeers is the avatar stack of who else has this board open. Initials
// only: the roster is a handful of colleagues, and a name per head would crowd
// out the card buttons. Colour comes from the server so the same person reads
// the same in everyone's view.
function BoardPeers({ peers }: { peers: BoardPeer[] }) {
  const { t } = useTranslation('whiteboards')
  if (peers.length === 0) return null
  return (
    <Tooltip title={t('canvas.peersHere', { names: peers.map((p) => p.name).join(', ') })}>
      <Stack direction="row" spacing={-0.5} sx={{ mr: 1 }}>
        {peers.slice(0, 5).map((p) => (
          <Box
            key={p.userId || p.name}
            sx={{
              width: 22,
              height: 22,
              borderRadius: '50%',
              bgcolor: p.color,
              color: '#fff',
              fontSize: 10,
              fontWeight: 600,
              display: 'grid',
              placeItems: 'center',
              border: 2,
              borderColor: 'background.paper',
            }}
          >
            {firstGrapheme(p.name).toLocaleUpperCase()}
          </Box>
        ))}
        {peers.length > 5 && (
          <Typography variant="caption" color="text.secondary" sx={{ alignSelf: 'center', pl: 1 }}>
            +{peers.length - 5}
          </Typography>
        )}
      </Stack>
    </Tooltip>
  )
}
