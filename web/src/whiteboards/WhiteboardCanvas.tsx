import { faDiagramProject, faListCheck, faPaperclip, faSitemap } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Alert, Box, Button, CircularProgress, Snackbar, Stack, Tooltip, Typography } from '@mui/material'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  FrameShapeUtil,
  Tldraw,
  createBindingId,
  createShapeId,
  createTLStore,
  defaultBindingUtils,
  defaultShapeUtils,
  loadSnapshot,
  type Editor,
  type TLShapeId,
} from 'tldraw'
import 'tldraw/tldraw.css'
import { getAssetUrlsByMetaUrl } from '@tldraw/assets/urls'
import { api } from '../api/client'
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
import { RemountingMinimap, useCanvasGeometry } from './canvasGeometry'
import { useBoardEvents, type BoardPeer } from './useBoardEvents'
import { useBoardSync } from './useBoardSync'
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

// Only the minimap is replaced, and only so it can re-measure itself when the
// canvas moves. Module scope: a new object per render would remount tldraw's
// whole UI.
const TLDRAW_COMPONENTS = { Minimap: RemountingMinimap }

// tldraw ships no plain 'pt' — only pt-pt and pt-br — and our catalogues are
// European Portuguese, so that one needs mapping. Everything else this app
// speaks, tldraw speaks under the same code.
const TLDRAW_LOCALE: Record<string, string> = { pt: 'pt-pt' }
const tldrawLocale = (appLocale: string) => TLDRAW_LOCALE[appLocale] ?? appLocale

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
  const [pending, setPending] = useState<Pending>(null)
  // The assembly flow makes two APS calls (details → occurrences), so it shows a
  // busy state on its button; `notice` surfaces the after-the-fact summary
  // (children skipped for having no design, or a plain part with none at all).
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  // The revision the document was loaded at, handed to the sync hook as its
  // starting point. State rather than a ref: the hook must re-anchor when it
  // changes, which only happens on load.
  const [loadedRev, setLoadedRev] = useState(0)

  const editorRef = useRef<Editor | null>(null)
  // The canvas wrapper, watched for on-screen geometry changes (see
  // canvasGeometry.tsx).
  const wrapRef = useRef<HTMLDivElement | null>(null)
  // Set once a board's document has loaded, cleared when the view has actually
  // been fitted to it. A ref because the attempt runs from callbacks, and
  // because re-rendering on it would say nothing to the user.
  const fitPending = useRef(false)

  // Fit the view to the board's content, once, when it first opens. Without it
  // a board opens at the origin at 100%, which for anything drawn away from
  // 0,0 is a blank canvas — the work is there, just off screen.
  //
  // Three things must all be true before it can run, and they arrive in an
  // order that isn't guaranteed: the editor has mounted, the document has
  // loaded, and the canvas is actually on screen. That last one is not
  // hypothetical here — a board mounts as soon as it is selected, even while
  // its project tab is parked off-screen by the tab Slide, and fitting against
  // a viewport that isn't where it will end up gives the wrong zoom. So each
  // arrival calls this, and whichever is last does the work.
  const tryFit = useCallback(() => {
    if (!fitPending.current) return
    const editor = editorRef.current
    const rect = wrapRef.current?.getBoundingClientRect()
    if (!editor || !rect || rect.width < 1 || rect.height < 1) return
    fitPending.current = false
    // An empty board has nothing to fit to; leave it at the default camera
    // rather than zooming to an arbitrary bound.
    if (editor.getCurrentPageShapeIds().size === 0) return
    editor.zoomToFit()
    // Fitting alone would magnify a board holding one small shape to tldraw's
    // maximum zoom, which reads as "something is broken" rather than "here is
    // your board". Zooming OUT to show everything is the point; zooming past
    // life size is not, so cap it and keep the content centred.
    const bounds = editor.getCurrentPageBounds()
    if (bounds && editor.getZoomLevel() > 1) {
      editor.zoomToBounds(bounds, { targetZoom: 1 })
    }
  }, [])

  // Also re-anchors the minimap: it caches its screen rect from a
  // ResizeObserver, which never sees the tab Slide's transform, and a stale
  // rect makes every minimap click land far off (see canvasGeometry.tsx).
  useCanvasGeometry(wrapRef, tryFit)

  // ---- load once per board ----
  useEffect(() => {
    let cancelled = false
    fitPending.current = true
    setLoading(true)
    setLoadError(null)
    api
      .whiteboardDoc(projectId, boardId)
      .then(({ doc, rev }) => {
        if (cancelled) return
        setLoadedRev(rev)
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
        tryFit()
      })
    return () => {
      cancelled = true
    }
  }, [projectId, boardId, store, tryFit])

  // Live editing replaces the old autosave entirely. The canvas no longer
  // writes whole documents on a timer; it sends what changed, the server
  // orders it, and everyone on the board receives it. Persistence is the
  // server's job now — the room behind the board writes on its own debounce.
  //
  // The whole-document PUT has not gone away, but nothing here calls it: it is
  // the import/overwrite path, and a client falling back to it on a bad
  // connection would reinstate exactly the silent clobber this replaced.
  const { sync, onFrame, setLive, onReplaced } = useBoardSync({
    store,
    projectId,
    boardId,
    canWrite,
    ready: !loading && !loadError,
    initialRev: loadedRev,
  })

  // Follow the app's light/dark mode and language rather than tldraw's own
  // defaults — left to itself it takes the colour scheme from the OS and the
  // language from the browser, so the canvas could sit in German inside an
  // English app.
  useEffect(() => {
    editorRef.current?.user.updateUserPreferences({ colorScheme: mode, locale })
  }, [mode, locale])

  // ---- awareness ----

  const { peers } = useBoardEvents(projectId, boardId, {
    onPatch: onFrame,
    onReplaced,
    onLive: setLive,
  })
  // Everyone on the board except this session's own user — the header shows
  // "who else", and seeing yourself in it is just noise.
  const others = peers.filter((p) => !me?.id || p.userId !== me.id)

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
            {syncLabel(t, sync)}
          </Typography>
        </Stack>
      )}

      {/* Offline is not "your work is lost" — the editor still holds it and the
          queue drains on reconnect — but it must be visible, because until it
          clears nobody else can see what is being drawn. */}
      {sync.status === 'offline' && (
        <Alert severity="warning" sx={{ borderRadius: 0, py: 0.25 }}>
          {t('canvas.offline')}
        </Alert>
      )}

      {/* data-fls-drop-owner: the canvas handles its own drops (tldraw turns a
          dropped file into a shape and stops the event propagating), so the
          app-wide upload drop handler in state/uploads.tsx stays out of it —
          otherwise a drop here also opened the upload dialog, and the drop it
          never saw left the upload overlay stuck over the whole window. */}
      <Box
        ref={wrapRef}
        // --fls-canvas hands the resolved canvas colour to whiteboard.css,
        // which is the only stylesheet in the app (tldraw is themed through CSS
        // variables and cannot be reached with sx). Without this the board
        // would keep the literal fallback baked into that file while every
        // other canvas followed the theme.
        sx={{
          flex: 1,
          minHeight: 0,
          position: 'relative',
          '--fls-canvas': (t) => t.palette.background.canvas,
        }}
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
            components={TLDRAW_COMPONENTS}
            onMount={(editor) => {
              editorRef.current = editor
              editor.user.updateUserPreferences({ colorScheme: mode, locale })
              editor.updateInstanceState({ isReadonly: !canWrite })
              tryFit()
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

// syncLabel says where this canvas stands with the server, in the place the
// old save indicator used to be. "Synced" is the resting state, so it reads as
// reassurance rather than activity.
function syncLabel(t: TFunction, sync: { status: string; pending: boolean }): string {
  if (sync.status === 'error') return t('canvas.syncError')
  if (sync.status === 'offline') return t('canvas.offlineShort')
  if (sync.pending) return t('canvas.syncing')
  if (sync.status === 'connecting') return t('canvas.connecting')
  return t('canvas.synced')
}
