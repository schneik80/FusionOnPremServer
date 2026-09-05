import {
  Box,
  Checkbox,
  CircularProgress,
  FormControlLabel,
  IconButton,
  List,
  ListItem,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Paper,
  Slide,
  Stack,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Tabs,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha, useTheme } from '@mui/material/styles'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faArrowsRotate, faBug, faCircleCheck, faClock } from '@fortawesome/free-solid-svg-icons'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  useBOM,
  useClassify,
  useCustomProperties,
  useDescendants,
  useDesignActivity,
  useDrawings,
  useLocalRefs,
  useMeta,
  useRollupActivity,
  useItemDetails,
  useProperties,
  useThumbnail,
  useUses,
  useWhereUsed,
} from '../api/queries'
import type { ComponentRef, Details, DrawingRef, Item, LocalRef, Measure } from '../api/types'
import { FUSION_NATIVE_KINDS, LOCAL_REF_KINDS, kindFromTypename } from '../api/types'
import { documentState, type DocumentState } from '../api/documentState'
import { DocumentActions } from './DocumentActions'
import { thumbnailSrc } from '../api/thumbnails'
import { useNav } from '../state/nav'
import { useGoToDocument } from '../state/goto'
import { ItemIcon } from './entityIcons'
import { TAB_SLIDE_TIMEOUT } from './motion'
import { batchKindLabel, docStateLabel, localRefKindLabel, localRefViaLabel, taskStatusLabel } from '../i18n/enums'
import ActivityHeatmap from './ActivityHeatmap'
import HistoryTimeline from './history/HistoryTimeline'
import PermissionsExplorer from './PermissionsExplorer'
import RelationGraph, { type GraphNode } from './RelationGraph'
import { RefTokenDialog, useRefTokenNavigate } from './RefTokenDialog'
import { ViewerTab } from './viewers/ViewerTab'

// The Details metadata is now always shown (in the header, beside the
// thumbnail), so it is no longer a tab. The remaining tabs:
type TabKey =
  | 'preview'
  | 'history'
  | 'activity'
  | 'properties'
  | 'bom'
  | 'uses'
  | 'whereUsed'
  | 'drawings'
  | 'permissions'

// Catalog keys per tab (details namespace); rendered through t() below.
const TAB_LABEL: Record<TabKey, string> = {
  preview: 'details.tab.preview',
  history: 'details.tab.history',
  activity: 'details.tab.activity',
  properties: 'details.tab.properties',
  bom: 'details.tab.bom',
  uses: 'details.tab.uses',
  whereUsed: 'details.tab.whereUsed',
  drawings: 'details.tab.drawings',
  permissions: 'details.tab.permissions',
}

// Designs get the full set; configured designs add Properties + BOM; drawings
// get Uses (the source design). Permissions (the project's groups + roles)
// applies to any document. Uploaded (non-native) files lead with a Preview tab —
// image/pdf/video/text viewers, or a download fallback — since they have no
// design data.
//
// Every other kind (schematic/pcb/ecad, and any kind added server-side that this
// build predates) keeps History as its default but still offers Preview, so a
// document is never stranded on a single tab just because its kind isn't
// enumerated here. Preview is second, not first: those kinds have real version
// history, while their extensions match no viewer (see viewers/kind.ts), so
// leading with a download fallback would bury the useful tab.
function tabsFor(kind: string): TabKey[] {
  if (kind === 'design')
    return ['history', 'activity', 'properties', 'bom', 'uses', 'whereUsed', 'drawings', 'permissions']
  if (kind === 'configured') return ['history', 'activity', 'properties', 'bom', 'permissions']
  if (kind === 'drawing') return ['history', 'activity', 'uses', 'permissions']
  if (kind === 'unknown') return ['preview', 'history']
  return ['history', 'preview']
}

export function DetailsPanel() {
  const { t } = useTranslation('details')
  const nav = useNav()
  const selected = nav.selected

  return (
    <Paper
      square
      variant="outlined"
      sx={{
        flex: 1,
        minWidth: 320,
        display: 'flex',
        flexDirection: 'column',
        borderTop: 0,
        borderBottom: 0,
        borderRight: 0,
        overflow: 'hidden',
      }}
    >
      {selected ? (
        <SelectedDetails
          key={selected.id}
          hubId={nav.hubId}
          item={selected}
          projectAltId={nav.project?.altId}
        />
      ) : (
        <Box
          sx={{
            height: '100%',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            p: 3,
          }}
        >
          <Typography variant="body2" color="text.secondary">
            {t('details.selectDocument')}
          </Typography>
        </Box>
      )}
    </Paper>
  )
}

function SelectedDetails({
  hubId,
  item: selected,
  projectAltId,
}: {
  hubId: string | null
  item: Item
  projectAltId?: string
}) {
  const { t } = useTranslation('details')
  const nav = useNav()

  // Whatever the caller believed this document's kind was, the details query's
  // GraphQL typename settles it — and this panel decides everything off kind:
  // which tabs exist, whether Open / Insert / Archive are offered, whether a
  // thumbnail is even attempted.
  //
  // Callers get it wrong for real reasons, not carelessness: a card token
  // captured a kind months ago, and the DM-backed hub browser derives one from
  // the file extension, which a Fusion Team design does not have. Rather than
  // trust each entry point to be right, this is the one place that has to be.
  const detailsQ = useItemDetails(hubId, selected.id)
  const resolvedKind = kindFromTypename(detailsQ.data?.typename) ?? selected.kind
  const item = useMemo(
    () => (resolvedKind === selected.kind ? selected : { ...selected, kind: resolvedKind }),
    [selected, resolvedKind],
  )

  const available = useMemo(() => tabsFor(item.kind), [item.kind])
  // Open the tab a cross-document jump requested (nav.selectedTab), if valid for
  // this item; else default to History. Read once at mount — this component
  // remounts (key={item.id}) on every selection change.
  const [tab, setTab] = useState<TabKey>(
    nav.selectedTab && available.includes(nav.selectedTab as TabKey)
      ? (nav.selectedTab as TabKey)
      : available[0],
  )

  // Reset to a valid tab whenever the tab set changes. Two things change it:
  // a new selection (key={item.id} already remounts this, but guard anyway),
  // and the kind resolving from the details query — a document that arrived
  // hinted as an uploaded file and turned out to be a design starts on Preview
  // and has to be moved off it. Falls back to the first available tab.
  useEffect(() => {
    if (!available.includes(tab)) setTab(available[0])
  }, [available, tab])

  // Slide the tab body in the direction moved along the strip, matching the
  // project tabs. Index is taken from `available` rather than a fixed list
  // because the tab set varies by item kind (a design has eight, a plain file
  // two), so only the position actually on screen gives the right direction.
  const tabIndex = available.indexOf(tab)
  const prevTabIndex = useRef(tabIndex)
  const forward = tabIndex >= prevTabIndex.current
  useEffect(() => {
    prevTabIndex.current = tabIndex
  }, [tabIndex])

  // The body mounts only the active tab — every other tab is unrendered, and
  // each fetches on mount — so this cannot cross-slide the way ProjectPanel
  // does without firing every tab's APS queries at once. It animates the
  // entering tab only: `key` remounts the pane on change, and `appear` runs the
  // transition on that mount.
  const [tabSlot, setTabSlot] = useState<HTMLDivElement | null>(null)

  // Animate real tab changes only, not the first paint: selecting a document
  // already slides this whole panel in from BrowserStage, and sliding the body
  // inside it at the same time reads as two competing animations.
  const didMount = useRef(false)
  useEffect(() => {
    didMount.current = true
  }, [])

  const cvId = item.componentVersionId || detailsQ.data?.rootComponentVersionId
  // Lazy assembly/part classification (cached, shared with the Contents column);
  // used to refine the Type label to "3D Design — Assembly" / "… — Part".
  const classifyQ = useClassify(cvId)
  const subtype = classifyQ.data?.subtype || item.subtype

  // Force-refresh every query belonging to this document. The document's data
  // is spread across keys identified by either its item id (details, drawings,
  // activity, location) or its component-version id (thumbnail, classify,
  // properties, BOM, uses, where-used, descendants); invalidating by a predicate
  // that matches either id refetches the visible queries (details, thumbnail,
  // active tab) immediately and marks the rest stale so they reload fresh when
  // next viewed.
  const qc = useQueryClient()
  const refresh = () => {
    void qc.invalidateQueries({
      predicate: (q) =>
        Array.isArray(q.queryKey) &&
        q.queryKey.some((part) => part === item.id || (!!cvId && part === cvId)),
    })
  }

  // Developer-only: when the server runs with -v, expose a probe button that
  // opens the version/milestone field-discovery endpoint for THIS document in a
  // new tab (the same session cookie authorizes it), so we never hand-build URLs.
  const debug = useMeta().data?.debug ?? false
  const openVersionProbe = () => {
    if (!hubId) return
    const q = new URLSearchParams({ hubId, itemId: item.id }).toString()
    window.open(`/api/debug/version-probe?${q}`, '_blank', 'noopener')
  }
  // The sibling probe: where (if anywhere) the public schema exposes a design's
  // non-save history — the gate for the History tab's "Show other changes".
  const openHistoryProbe = () => {
    if (!hubId) return
    const q = new URLSearchParams({ hubId, itemId: item.id }).toString()
    window.open(`/api/debug/history-probe?${q}`, '_blank', 'noopener')
  }

  // Lifecycle state badge (WIP / Version / Released - Rev X), derived once from
  // the details payload; see ../api/documentState.
  const docState = detailsQ.data ? documentState(detailsQ.data) : null

  return (
    <>
      {/* Header: a compact top bar — a small rounded thumbnail on the far left,
          then the document name + lifecycle-state badge + actions, with the
          always-visible metadata below. */}
      <Box sx={{ px: 2, pt: 1.5, pb: 1.5, borderBottom: 1, borderColor: 'divider' }}>
        <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start' }}>
          <Thumbnail
            kind={item.kind}
            itemId={item.id}
            cvId={cvId}
            name={item.name}
            projectAltId={projectAltId}
            size={80}
          />
          {/* containerType makes this column a query container so the metadata
              grid below can switch to two columns based on THIS panel's width. */}
          <Box sx={{ flex: 1, minWidth: 0, containerType: 'inline-size' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.75 }}>
              <Typography
                variant="h6"
                noWrap
                title={item.name}
                sx={{ flex: '0 1 auto', minWidth: 0, lineHeight: 1.2 }}
              >
                {item.name}
              </Typography>
              {docState && <StateBadge state={docState} />}
              <Box sx={{ flex: 1 }} />
              {/* Open / Insert / Archive — Fusion-native documents only; the
                  component decides and renders nothing otherwise. */}
              <DocumentActions item={item} projectAltId={projectAltId} />
              {debug && (
                /* eslint-disable i18next/no-literal-string -- dev-only (meta.debug gated) */
                <>
                  <Tooltip title="Probe version/milestone fields (dev)">
                    <IconButton
                      size="small"
                      onClick={openVersionProbe}
                      disabled={!hubId}
                      aria-label="Probe version fields"
                      sx={{ flexShrink: 0, color: 'warning.main' }}
                    >
                      <FontAwesomeIcon icon={faBug} style={{ fontSize: 14 }} />
                    </IconButton>
                  </Tooltip>
                  <Tooltip title="Probe history-change fields (dev)">
                    <IconButton
                      size="small"
                      onClick={openHistoryProbe}
                      disabled={!hubId}
                      aria-label="Probe history fields"
                      sx={{ flexShrink: 0, color: 'warning.main' }}
                    >
                      <FontAwesomeIcon icon={faClock} style={{ fontSize: 14 }} />
                    </IconButton>
                  </Tooltip>
                </>
                /* eslint-enable i18next/no-literal-string */
              )}
              <Tooltip title={t('details.refreshDocument')}>
                <IconButton
                  size="small"
                  onClick={refresh}
                  disabled={detailsQ.isFetching}
                  aria-label={t('details.refreshDocument')}
                  sx={{ flexShrink: 0 }}
                >
                  <FontAwesomeIcon icon={faArrowsRotate} spin={detailsQ.isFetching} style={{ fontSize: 14 }} />
                </IconButton>
              </Tooltip>
            </Box>
            <DetailsSummary
              query={detailsQ.data}
              kind={item.kind}
              subtype={subtype}
              loading={detailsQ.isLoading}
              error={detailsQ.error as Error | null}
            />
          </Box>
        </Box>
      </Box>

      <Tabs
        value={tab}
        onChange={(_, v: TabKey) => setTab(v)}
        variant="scrollable"
        scrollButtons="auto"
        sx={{ minHeight: 40, borderBottom: 1, borderColor: 'divider', flexShrink: 0 }}
      >
        {available.map((tk) => (
          <Tab key={tk} value={tk} label={t(TAB_LABEL[tk])} sx={{ minHeight: 40, py: 0 }} />
        ))}
      </Tabs>

      <Box ref={setTabSlot} sx={{ flex: 1, minHeight: 0, overflow: 'hidden', position: 'relative' }}>
        <Slide
          key={tab}
          direction={forward ? 'left' : 'right'}
          in
          appear={didMount.current}
          container={tabSlot}
          timeout={TAB_SLIDE_TIMEOUT}
        >
          {/* Scrolling lives on the sliding pane, not the clipping slot, so each
              tab keeps its own scroll and the transform never drags a scrollbar
              across the panel. */}
          <Box sx={{ position: 'absolute', inset: 0, overflowY: 'auto', p: 2 }}>
            {tab === 'preview' && (
              <ViewerTab item={item} details={detailsQ.data} dmProjectId={projectAltId} />
            )}
            {tab === 'history' && (
              <HistoryTab
                query={detailsQ.data}
                loading={detailsQ.isLoading}
                error={detailsQ.error as Error | null}
                hubId={hubId}
                itemId={item.id}
              />
            )}
            {tab === 'activity' && (
              <ActivityTab
                hubId={hubId}
                itemId={item.id}
                cvId={cvId}
                subtype={subtype}
                active={tab === 'activity'}
              />
            )}
            {tab === 'properties' && <PropertiesTab cvId={cvId} active />}
            {tab === 'bom' && <BOMTab cvId={cvId} active />}
            {tab === 'uses' && <UsesTab item={item} hubId={hubId} cvId={cvId} active />}
            {tab === 'whereUsed' && <WhereUsedTab item={item} hubId={hubId} cvId={cvId} active />}
            {tab === 'drawings' && <DrawingsTab hubId={hubId} designItemId={item.id} active />}
            {tab === 'permissions' && <PermissionsExplorer hubId={hubId} item={item} />}
          </Box>
        </Slide>
      </Box>
    </>
  )
}

// THUMBNAIL_POLL_TIMEOUT_MS bounds how long we wait on a still-generating
// (PENDING) thumbnail before giving up — APS generates thumbnails on demand and
// some never resolve, so without this a stuck design spins and re-polls APS
// every 2s forever.
const THUMBNAIL_POLL_TIMEOUT_MS = 30_000

// Thumbnail renders the component's preview image, sitting to the right of the
// details metadata. Designs and configured designs carry a componentVersionId
// for the MFGDM thumbnail. Drawings load a Model Derivative preview keyed by
// item id + project altId. For everything else, nothing is shown.
function Thumbnail({
  kind,
  itemId,
  cvId,
  name,
  projectAltId,
  size = 200,
}: {
  kind: string
  itemId: string
  cvId?: string
  name: string
  projectAltId?: string
  size?: number
}) {
  const { t } = useTranslation('details')
  const isDrawing = kind === 'drawing'
  const isDesign = kind === 'design' || kind === 'configured'

  // For designs: poll MFGDM thumbnail via cvId.
  const [gaveUp, setGaveUp] = useState(false)
  // For drawings: the .f2d preview can 404 (no native file / extraction failed).
  // Track that declaratively so a failed <img> renders nothing instead of
  // imperatively swapping src — mutating a React-controlled src fights
  // reconciliation and loops into a flicker.
  const [drawingFailed, setDrawingFailed] = useState(false)
  const q = useThumbnail(isDesign ? cvId : undefined, isDesign && !!cvId && !gaveUp)
  const status = q.data?.status

  useEffect(() => {
    setGaveUp(false)
    if (!cvId || status === 'SUCCESS' || status === 'FAILED') return
    const timer = window.setTimeout(() => setGaveUp(true), THUMBNAIL_POLL_TIMEOUT_MS)
    return () => window.clearTimeout(timer)
  }, [cvId, status])

  if (!isDesign && !isDrawing) return null

  // For designs: no image if hard error, failed thumbnail, or we gave up waiting.
  if (isDesign && (q.isError || status === 'FAILED' || gaveUp)) return null
  // For drawings: no image once the preview request has failed. A drawing's
  // item id is not a componentVersionId, so there's no MFGDM thumbnail to fall
  // back to — show nothing, mirroring the design failure path above.
  if (isDrawing && drawingFailed) return null

  const designReady = isDesign && status === 'SUCCESS'
  const drawingImageUrl = isDrawing ? thumbnailSrc({ kind, itemId, projectAltId }) : null

  const showLoading = isDrawing && !!projectAltId && !drawingImageUrl
  const showContent = designReady || (isDrawing && !!drawingImageUrl)

  return (
    <Box
      sx={{
        flexShrink: 0,
        width: size,
        maxWidth: size,
        aspectRatio: '1 / 1',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        bgcolor: 'action.hover',
        // Rounded container with clipping so the preview presents cleanly.
        borderRadius: 1.5,
        overflow: 'hidden',
      }}
    >
      {showContent ? (
        <Box
          component="img"
          src={isDesign ? thumbnailSrc({ kind, cvId })! : drawingImageUrl!}
          alt={t('details.previewAlt', { name })}
          sx={{ width: '100%', height: '100%', objectFit: 'contain' }}
          onError={() => {
            if (isDrawing) setDrawingFailed(true)
          }}
        />
      ) : showLoading ? (
        <CircularProgress size={Math.max(16, Math.round(size * 0.3))} />
      ) : null}
    </Box>
  )
}

// StateBadge renders a small pill showing the document's lifecycle state
// (WIP / Version / Released - Rev X). Colors key off the theme: neutral for WIP,
// the brand accent for a milestone version, success for a release.
function StateBadge({ state }: { state: DocumentState }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const color =
    state.kind === 'released'
      ? theme.palette.success.main
      : state.kind === 'version'
        ? theme.palette.primary.main
        : theme.palette.text.secondary
  return (
    <Box
      component="span"
      sx={{
        flexShrink: 0,
        display: 'inline-flex',
        alignItems: 'center',
        gap: 0.5,
        height: 20,
        px: 0.75,
        borderRadius: 1,
        bgcolor: alpha(color, 0.16),
        color,
        fontSize: 10.5,
        fontWeight: 700,
        letterSpacing: 0.4,
        textTransform: 'uppercase',
        whiteSpace: 'nowrap',
        lineHeight: 1,
      }}
    >
      {state.kind === 'released' && <FontAwesomeIcon icon={faCircleCheck} style={{ fontSize: 10 }} />}
      {docStateLabel(t, state)}
    </Box>
  )
}

function fmtDate(s?: string): string {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString()
}

// typeLabel renders a friendly document type, appending the lazily-classified
// Part/Assembly to designs — e.g. "3D Design — Assembly". Falls back to the raw
// GraphQL typename for kinds we don't have a friendly name for.
function typeLabel(t: TFunction, kind: string, typename?: string, subtype?: string): string {
  const base =
    kind === 'design'
      ? t('details.type3dDesign')
      : kind === 'configured'
        ? t('details.typeConfiguredDesign')
        : kind === 'drawing'
          ? t('details.typeDrawing')
          : typename || kind || t('details.typeDocument')
  const sub =
    subtype === 'assembly' ? t('details.subtypeAssembly') : subtype === 'part' ? t('details.subtypePart') : ''
  return sub ? t('details.typeWithSubtype', { base, sub }) : base
}

// LabelGrid renders a two-column label/value grid, dropping empty rows.
function LabelGrid({ rows }: { rows: Array<[string, ReactNode]> }) {
  const present = rows.filter(([, v]) => v !== undefined && v !== '' && v !== null)
  if (present.length === 0) return null
  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: 'minmax(84px, auto) 1fr',
        columnGap: 2,
        rowGap: 0.75,
      }}
    >
      {present.map(([label, value]) => (
        <Box key={label} sx={{ display: 'contents' }}>
          <Typography variant="caption" color="text.secondary" sx={{ pt: 0.25 }}>
            {label}
          </Typography>
          <Typography variant="body2" sx={{ wordBreak: 'break-word' }}>
            {value}
          </Typography>
        </Box>
      ))}
    </Box>
  )
}

// DetailsSummary is the always-visible metadata block in the header (formerly
// the Details tab).
function DetailsSummary({
  query,
  kind,
  subtype,
  loading,
  error,
}: {
  query?: Details
  kind: string
  subtype?: string
  loading: boolean
  error: Error | null
}) {
  const { t } = useTranslation('details')
  if (loading) return <TabSpinner />
  if (error) return <TabError error={error} />
  if (!query) return <TabEmpty text={t('details.noDetails')} />

  // Identity / spec fields stay in the first column; the version + authorship
  // fields move into a second column once the details panel is wide enough,
  // compacting the header height. See the container query below.
  const primaryRows: Array<[string, ReactNode]> = [
    [t('details.fieldType'), typeLabel(t, kind, query.typename, subtype)],
    [t('details.fieldPartNumber'), query.partNumber],
    [t('details.fieldDescription'), query.partDesc],
    [t('details.fieldMaterial'), query.material],
    [t('details.fieldExtension'), FUSION_NATIVE_KINDS.has(kind) ? undefined : query.extensionType],
  ]
  const secondaryRows: Array<[string, ReactNode]> = [
    [
      t('details.fieldVersion'),
      query.versionNumber ? t('details.versionNumber', { number: query.versionNumber }) : undefined,
    ],
    [t('details.fieldCreated'), query.createdOn ? `${fmtDate(query.createdOn)} · ${query.createdBy ?? ''}` : undefined],
    [t('details.fieldModified'), query.modifiedOn ? `${fmtDate(query.modifiedOn)} · ${query.modifiedBy ?? ''}` : undefined],
  ]
  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: '1fr',
        columnGap: 3,
        rowGap: 0.75,
        // Two side-by-side columns once the header's detail column is wide
        // enough (container query on the header right column). Below the
        // threshold the two groups simply stack.
        '@container (min-width: 480px)': { gridTemplateColumns: '1fr 1fr', alignItems: 'start' },
      }}
    >
      <LabelGrid rows={primaryRows} />
      <LabelGrid rows={secondaryRows} />
    </Box>
  )
}

// PropertiesTab shows the component version's physical (mass) properties from
// the v2 API. Generation is async, so it polls while computing.
// SectionLabel is a small uppercase heading for the Properties tab sections.
function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <Typography variant="overline" sx={{ display: 'block', color: 'text.secondary', lineHeight: 1.6 }}>
      {children}
    </Typography>
  )
}

// PropertiesTab shows the component's custom/standard named properties (when
// any are available) above its physical/mass properties.
function PropertiesTab({ cvId, active }: { cvId?: string; active: boolean }) {
  const { t } = useTranslation('details')
  const cpQ = useCustomProperties(cvId, active)
  const customRows: Array<[string, ReactNode]> = (cpQ.data ?? []).map((p) => [p.name, p.value])

  return (
    <Stack spacing={2}>
      {customRows.length > 0 && (
        <Box>
          <SectionLabel>{t('details.sectionProperties')}</SectionLabel>
          <LabelGrid rows={customRows} />
        </Box>
      )}
      <Box>
        <SectionLabel>{t('details.sectionPhysical')}</SectionLabel>
        <PhysicalProperties cvId={cvId} active={active} />
      </Box>
    </Stack>
  )
}

function PhysicalProperties({ cvId, active }: { cvId?: string; active: boolean }) {
  const { t } = useTranslation('details')
  const q = useProperties(cvId, active)
  if (q.isLoading) return <TabSpinner />
  if (q.error) return <TabError error={q.error as Error} />
  const p = q.data
  if (!p) return <TabEmpty text={t('details.noProperties')} />

  if (p.status !== 'COMPLETED') {
    return q.isFetching ? (
      <Stack direction="row" spacing={1.5} alignItems="center" sx={{ py: 2 }}>
        <CircularProgress size={18} />
        <Typography variant="body2" color="text.secondary">
          {t('details.computingPhysical')}
        </Typography>
      </Stack>
    ) : (
      <TabEmpty text={t('details.physicalUnavailable')} />
    )
  }

  // APS returns full-precision floats (e.g. "25.68624402467584"); round to 4
  // significant figures for display, leaving non-numeric values untouched.
  const round = (s: string) => {
    const n = Number(s)
    return Number.isFinite(n) ? String(Number(n.toPrecision(4))) : s
  }
  const fmt = (m: Measure) =>
    m.display ? `${round(m.display)}${m.units ? ` ${m.units}` : ''}` : undefined
  const bbox =
    p.bboxLength.display && p.bboxWidth.display && p.bboxHeight.display
      ? `${round(p.bboxLength.display)} × ${round(p.bboxWidth.display)} × ${round(p.bboxHeight.display)}${
          p.bboxLength.units ? ` ${p.bboxLength.units}` : ''
        }`
      : undefined

  const rows: Array<[string, ReactNode]> = [
    [t('details.mass'), fmt(p.mass)],
    [t('details.volume'), fmt(p.volume)],
    [t('details.surfaceArea'), fmt(p.area)],
    [t('details.density'), fmt(p.density)],
    [t('details.boundingBox'), bbox],
  ]
  if (!rows.some(([, v]) => !!v)) return <TabEmpty text={t('details.noPhysicalProperties')} />
  return <LabelGrid rows={rows} />
}

// BOMTab shows a flat bill of materials: one row per unique sub-component with
// a quantity (the occurrence count — the v2 API has no explicit quantity field).
function BOMTab({ cvId, active }: { cvId?: string; active: boolean }) {
  const { t } = useTranslation('details')
  const q = useBOM(cvId, active)
  if (q.isLoading) return <TabSpinner />
  if (q.error) return <TabError error={q.error as Error} />
  const rows = q.data ?? []
  if (rows.length === 0) return <TabEmpty text={t('details.noBom')} />

  const total = rows.reduce((n, r) => n + r.quantity, 0)
  return (
    <>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 1 }}>
        {t('details.bomComponents', { count: rows.length })} · {t('details.bomOccurrences', { count: total })}
      </Typography>
      <Table size="small" sx={{ '& td, & th': { px: 1, py: 0.5 } }}>
        <TableHead>
          <TableRow>
            <TableCell>{t('details.colComponent')}</TableCell>
            <TableCell>{t('details.colPartNumber')}</TableCell>
            <TableCell>{t('details.colMaterial')}</TableCell>
            <TableCell align="right">{t('details.colQty')}</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {rows.map((b) => (
            <TableRow key={b.componentVersionId}>
              <TableCell title={b.partDesc || undefined}>{b.name}</TableCell>
              <TableCell>{b.partNumber || '—'}</TableCell>
              <TableCell>{b.material || '—'}</TableCell>
              <TableCell align="right">{b.quantity}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </>
  )
}

// HistoryTab renders the item's version history as a stack of day rows, newest
// first, each split into a track per author on a 24-hour clock axis — or, with
// the thread checkbox, on one continuous chronological axis.
function HistoryTab({
  query,
  loading,
  error,
  hubId,
  itemId,
}: {
  query?: Details
  loading: boolean
  error: Error | null
  hubId: string | null
  itemId: string
}) {
  const { t } = useTranslation('details')
  if (loading) return <TabSpinner />
  if (error) return <TabError error={error} />
  const versions = query?.versions ?? []
  if (versions.length === 0) return <TabEmpty text={t('details.noVersionHistory')} />

  return <HistoryTimeline versions={versions} hubId={hubId} itemId={itemId} />
}

// componentRefsToNodes maps Uses/Where-Used refs to graph nodes, deduped by the
// related document's lineage id (repeated occurrences collapse to one node).
function componentRefsToNodes(refs: ComponentRef[]): GraphNode[] {
  const seen = new Set<string>()
  const out: GraphNode[] = []
  for (const r of refs) {
    if (!r.designItemId || seen.has(r.designItemId)) continue
    seen.add(r.designItemId)
    out.push({
      key: r.designItemId,
      navId: r.designItemId,
      cvId: r.id,
      name: r.designItemName || r.name,
      kind: 'design',
      secondary: [r.partNumber, r.material].filter(Boolean).join(' · ') || undefined,
    })
  }
  return out
}

function RelationStats({ text }: { text: ReactNode }) {
  return (
    <Box sx={{ borderTop: 1, borderColor: 'divider', pt: 1 }}>
      <Typography variant="caption" color="text.secondary">
        {text}
      </Typography>
    </Box>
  )
}

// UsesTab shows a design's sub-components (or a drawing's source design) as an
// interactive relationship graph; clicking a node jumps to that document's Uses tab.
function UsesTab({ item, hubId, cvId, active }: { item: Item; hubId: string | null; cvId?: string; active: boolean }) {
  const { t } = useTranslation('details')
  const isDrawing = item.kind === 'drawing'
  const goTo = useGoToDocument()
  const q = useUses({
    cvId: isDrawing ? undefined : cvId,
    hubId: isDrawing ? hubId ?? undefined : undefined,
    drawingItemId: isDrawing ? item.id : undefined,
    enabled: active && (isDrawing ? !!hubId : !!cvId),
  })
  if (q.isLoading) return <TabSpinner />
  if (q.error) return <TabError error={q.error as Error} />
  const nodes = componentRefsToNodes(q.data ?? [])
  return (
    <Stack spacing={1.5} sx={{ height: '100%' }}>
      {nodes.length === 0 ? (
        <TabEmpty text={isDrawing ? t('details.noSourceDesign') : t('details.noSubComponents')} />
      ) : (
        <RelationGraph
          focus={{ name: item.name, kind: item.kind, cvId, itemId: item.id }}
          relations={nodes}
          direction="down"
          onNavigate={(n) => goTo({ itemId: n.navId!, name: n.name, kind: n.kind, componentVersionId: n.cvId }, { tab: 'uses' })}
        />
      )}
      <RelationStats
        text={
          <>
            <Trans
              t={t}
              i18nKey={isDrawing ? 'details.usesSourceDesigns' : 'details.usesSubComponents'}
              count={nodes.length}
              components={{ b: <b /> }}
            />
            {' · '}
            {item.name} · {typeLabel(t, item.kind, undefined, item.subtype)}
          </>
        }
      />
    </Stack>
  )
}

// WHERE_USED_SOURCES are the parent kinds the graph can draw, in checkbox
// order. 'drawing' is the APS one (a second GraphQL call); the rest are local
// records — one request covering every checked local source, so unchecking one
// genuinely removes work rather than just hiding rows.
const WHERE_USED_SOURCES = ['drawing', ...LOCAL_REF_KINDS] as const
type WhereUsedSource = (typeof WHERE_USED_SOURCES)[number]

// Catalog keys for the checkbox labels (plural — they name a whole source).
// The kind line under an individual node uses the singular enum token instead.
const SOURCE_LABEL: Record<WhereUsedSource, string> = {
  drawing: 'details.source.drawing',
  task: 'details.source.task',
  chat: 'details.source.chat',
  whiteboard: 'details.source.whiteboard',
  job: 'details.source.job',
  batch: 'details.source.batch',
}

// localRefsToNodes maps our own records onto graph nodes. They're marked soft:
// a chat mention or a whiteboard card is a weaker relationship than a parent
// assembly's occurrence, and the graph says so with a dashed edge rather than
// letting the two read as equivalent.
function localRefsToNodes(refs: LocalRef[], t: TFunction): GraphNode[] {
  return refs.map((r) => {
    const context = [
      r.projectName,
      r.via ? localRefViaLabel(t, r.via) : undefined,
      r.status ? enumStatusLabel(t, r.kind, r.status) : undefined,
      r.author,
      r.detail,
    ]
      .filter(Boolean)
      .join(' · ')
    return {
      key: r.key,
      name: r.name,
      kind: r.kind,
      kindLabel: localRefKindLabel(t, r.kind),
      soft: true,
      count: r.count,
      openable: !!r.token,
      detail: context,
    }
  })
}

// enumStatusLabel renders the one status field a local ref carries, which
// means something different per kind (a task's workflow status, a batch's
// prove/production kind).
function enumStatusLabel(t: TFunction, kind: string, status: string): string {
  if (kind === 'task') return taskStatusLabel(t, status)
  if (kind === 'batch') return batchKindLabel(t, status)
  return status
}

// WhereUsedTab shows everything that references this document: the parent
// designs that use it, optionally its drawings, and — the local half — our own
// tasks, chat channels, whiteboards, job plans and batch runs that mention it.
// Each source has a checkbox; clicking a node jumps to a document's Where Used
// tab, or opens a local record's own dialog.
//
// Wiki pages are not a source. They're markdown files in Fusion Team rather
// than a local store, so scanning them would cost an APS download per page —
// the per-row fan-out this app never does (see handlers_localrefs.go).
function WhereUsedTab({ item, hubId, cvId, active }: { item: Item; hubId: string | null; cvId?: string; active: boolean }) {
  const { t } = useTranslation('details')
  const goTo = useGoToDocument()
  const navigateToken = useRefTokenNavigate()
  const [off, setOff] = useState<WhereUsedSource[]>([])
  const [openToken, setOpenToken] = useState<string | null>(null)
  const on = (s: WhereUsedSource) => !off.includes(s)
  const toggle = (s: WhereUsedSource) =>
    setOff((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]))

  const localSources = LOCAL_REF_KINDS.filter(on)
  const wuQ = useWhereUsed(cvId, active)
  const dwgQ = useDrawings(hubId, item.id, active && on('drawing'))
  const localQ = useLocalRefs(item.id, localSources, active)

  if (wuQ.isLoading) return <TabSpinner />
  if (wuQ.error) return <TabError error={wuQ.error as Error} />

  const parents = componentRefsToNodes(wuQ.data ?? [])
  const drawings: GraphNode[] = on('drawing')
    ? (dwgQ.data ?? []).map((d) => ({
        key: 'dwg:' + d.drawingItemId,
        navId: d.drawingItemId,
        name: d.name,
        kind: 'drawing',
        secondary: d.modifiedBy || undefined,
      }))
    : []
  const localRefs = localSources.length > 0 ? (localQ.data?.refs ?? []) : []
  const locals = localRefsToNodes(localRefs, t)
  const relations = [...parents, ...drawings, ...locals]
  const tokenByKey = new Map(localRefs.filter((r) => r.token).map((r) => [r.key, r.token!]))

  return (
    <Stack spacing={1} sx={{ height: '100%' }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', columnGap: 1.5 }}>
        {WHERE_USED_SOURCES.map((s) => (
          <FormControlLabel
            key={s}
            sx={{ m: 0 }}
            control={<Checkbox size="small" checked={on(s)} onChange={() => toggle(s)} />}
            label={<Typography variant="body2">{t(SOURCE_LABEL[s])}</Typography>}
          />
        ))}
        {localQ.isFetching ? <CircularProgress size={14} /> : null}
      </Box>
      {relations.length === 0 ? (
        // With local sources switched on the tab is no longer only about
        // parent designs, so the empty state must not claim it is.
        <TabEmpty
          text={
            localSources.length > 0 || on('drawing')
              ? t('details.noReferences')
              : t('details.notUsedByAnyDesign')
          }
        />
      ) : (
        <RelationGraph
          focus={{ name: item.name, kind: item.kind, cvId, itemId: item.id }}
          relations={relations}
          direction="up"
          onNavigate={(n) => {
            const token = tokenByKey.get(n.key)
            if (token) {
              // Some schemes navigate rather than opening a dialog (a
              // whiteboard); the rest raise theirs.
              if (navigateToken(token)) return
              setOpenToken(token)
              return
            }
            goTo({ itemId: n.navId!, name: n.name, kind: n.kind, componentVersionId: n.cvId }, { tab: 'whereUsed' })
          }}
        />
      )}
      {openToken ? <RefTokenDialog token={openToken} onClose={() => setOpenToken(null)} /> : null}
      <RelationStats
        text={
          <>
            <Trans t={t} i18nKey="details.whereUsedParents" count={parents.length} components={{ b: <b /> }} />
            {on('drawing') && drawings.length > 0 ? (
              <>
                {' · '}
                {t('details.whereUsedDrawings', { count: drawings.length })}
              </>
            ) : null}
            {locals.length > 0 ? (
              <>
                {' · '}
                {t('details.whereUsedLocal', { count: locals.length })}
              </>
            ) : null}
            {localQ.data?.truncated ? (
              <>
                {' · '}
                {t('details.whereUsedTruncated', { count: localQ.data.cap })}
              </>
            ) : null}
            {localQ.error ? (
              <>
                {' · '}
                {t('details.whereUsedLocalUnavailable')}
              </>
            ) : null}
            {' · '}
            {item.name} · {typeLabel(t, item.kind, undefined, item.subtype)}
          </>
        }
      />
    </Stack>
  )
}

// ActivityTab shows the design's change activity as an isometric heat map,
// sourced from the GraphQL-backed design activity report. "Roll up child changes"
// merges every descendant document's activity in (computed server-side).
//
// The descendant walk runs only for assemblies (per the folder-level
// classification), so a leaf part loads the tab without that cost.
function ActivityTab({
  hubId,
  itemId,
  cvId,
  subtype,
  active,
}: {
  hubId: string | null
  itemId: string
  cvId?: string
  subtype?: string
  active: boolean
}) {
  const { t } = useTranslation('details')
  const reportQ = useDesignActivity(active ? hubId : null, active ? itemId : null)

  // Only assemblies have children; gate the (potentially expensive) recursive
  // descendant enumeration on that so parts skip it entirely on tab load.
  const isAssembly = subtype === 'assembly'
  const descendantsQ = useDescendants(cvId, active && !!cvId && isAssembly)
  const childItemIds = useMemo(() => {
    const seen = new Set<string>()
    for (const u of descendantsQ.data ?? []) {
      const id = u.designItemId
      if (id && id !== itemId) seen.add(id)
    }
    return [...seen]
  }, [descendantsQ.data, itemId])
  const childCount = childItemIds.length
  const hasChildren = isAssembly && childCount > 0

  const [rollup, setRollup] = useState(false)

  // Roll-up is computed server-side (enumerate ids here, fan out + merge there)
  // so even a large assembly completes reliably in one request.
  const rollupQ = useRollupActivity(hubId, itemId, childItemIds, rollup && hasChildren)
  const rollupLoading =
    rollup && (descendantsQ.isLoading || rollupQ.isPending || rollupQ.isFetching)

  const report = rollup && !rollupLoading && rollupQ.data ? rollupQ.data : reportQ.data

  if (reportQ.isLoading) return <TabSpinner />
  if (reportQ.error) return <TabError error={reportQ.error as Error} />
  if (!report) return <TabEmpty text={t('details.noActivityRecorded')} />
  return (
    <ActivityHeatmap
      report={report}
      childCount={isAssembly ? childCount : undefined}
      rollup={hasChildren ? { checked: rollup, loading: rollupLoading, onChange: setRollup } : undefined}
    />
  )
}

function DrawingsTab({
  hubId,
  designItemId,
  active,
}: {
  hubId: string | null
  designItemId: string
  active: boolean
}) {
  const { t } = useTranslation('details')
  const q = useDrawings(hubId, designItemId, active)
  if (q.isLoading) return <TabSpinner />
  if (q.error) return <TabError error={q.error as Error} />
  const drawings = q.data ?? []
  if (drawings.length === 0) return <TabEmpty text={t('details.noDrawingsReference')} />
  return (
    <List dense disablePadding>
      {drawings.map((d: DrawingRef) => (
        <NavRow
          key={d.drawingItemId}
          itemId={d.drawingItemId}
          name={d.name}
          kind="drawing"
          secondary={`${fmtDate(d.modifiedOn)}${d.modifiedBy ? ` · ${d.modifiedBy}` : ''}`}
        />
      ))}
    </List>
  )
}

// NavRowIcon shows a document's preview (the server-cached image proxy) —
// designs via their component-version thumbnail, drawings via the Model
// Derivative preview — falling back to the kind icon on a miss.
function NavRowIcon({
  cvId,
  itemId,
  kind,
  projectAltId,
}: {
  cvId?: string
  itemId?: string
  kind: string
  projectAltId?: string
}) {
  const [failed, setFailed] = useState(false)
  const src = thumbnailSrc({ kind, cvId, itemId, projectAltId })
  if (src && !failed) {
    return (
      <ListItemIcon sx={{ minWidth: 36 }}>
        <Box
          component="img"
          src={src}
          alt=""
          onError={() => setFailed(true)}
          sx={{ width: 26, height: 26, objectFit: 'contain', borderRadius: 0.5, display: 'block' }}
        />
      </ListItemIcon>
    )
  }
  return (
    <ListItemIcon sx={{ minWidth: 36, color: 'text.secondary' }}>
      <ItemIcon item={{ kind }} style={{ fontSize: 16 }} />
    </ListItemIcon>
  )
}

// NavRow is a clickable row for the Uses / Where Used / Drawings tabs. It mirrors
// the Projects/Contents rows — a ListItemButton picks up the themed hover and
// selected highlight — and on click navigates the browser to that document by
// resolving its location (project + folder path), the flow the Pins dialog uses.
function NavRow({
  itemId,
  name,
  kind,
  componentVersionId,
  secondary,
}: {
  itemId?: string
  name: string
  kind: string
  componentVersionId?: string
  secondary?: string
}) {
  const nav = useNav()
  const goToDocument = useGoToDocument()
  const [busy, setBusy] = useState(false)
  const canNav = !!itemId && !!nav.hubId
  const selected = !!itemId && nav.selected?.id === itemId

  const goTo = async () => {
    if (!canNav || busy) return
    setBusy(true)
    try {
      await goToDocument({ itemId: itemId!, name, kind, componentVersionId })
    } catch {
      /* couldn't resolve the location — leave the user where they are */
    } finally {
      setBusy(false)
    }
  }

  return (
    <ListItem
      disablePadding
      secondaryAction={busy ? <CircularProgress size={14} sx={{ mr: 1 }} /> : undefined}
    >
      <ListItemButton selected={selected} onClick={goTo} disabled={!canNav} sx={{ py: 0.5 }}>
        <NavRowIcon cvId={componentVersionId} itemId={itemId} kind={kind} projectAltId={nav.project?.altId} />
        <ListItemText
          primary={name}
          secondary={secondary}
          primaryTypographyProps={{ variant: 'body2', noWrap: true, fontWeight: selected ? 600 : 400 }}
          secondaryTypographyProps={{ variant: 'caption' }}
        />
      </ListItemButton>
    </ListItem>
  )
}

const TabSpinner = () => (
  <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
    <CircularProgress size={22} />
  </Box>
)

const TabError = ({ error }: { error: Error }) => (
  <Typography variant="body2" color="error">
    {error.message}
  </Typography>
)

const TabEmpty = ({ text }: { text: string }) => (
  <Typography variant="body2" color="text.secondary">
    {text}
  </Typography>
)
