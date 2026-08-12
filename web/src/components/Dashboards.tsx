import {
  Box,
  Button,
  Chip,
  List,
  ListItemButton,
  Stack,
  Typography,
} from '@mui/material'
import { useTheme } from '@mui/material/styles'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faUsers } from '@fortawesome/free-solid-svg-icons'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import UserAvatar from './UserAvatar'
import type { TFunction } from 'i18next'
import { useQueries } from '@tanstack/react-query'
import { Cell, Pie, PieChart, ResponsiveContainer, Tooltip as RechartsTooltip } from 'recharts'
import { api } from '../api/client'
import {
  useFolderContents,
  useProjectContents,
  usePermissionsPath,
  usePins,
  useRollupActivity,
} from '../api/queries'
import type { Item, PermMember, Pin, ProjectGroup } from '../api/types'
import { roleLabel } from '../i18n/enums'
import { useGoToDocument } from '../state/goto'
import { useNav } from '../state/nav'
import ActivityHeatmap from './ActivityHeatmap'
import { DashboardShell, DONUT_PALETTE, Hint, WidgetCard } from './dashboard/shell'
import { ItemIcon } from './entityIcons'

// The project-level landing pane that fills the right slot of the browser when
// no document is selected: real widgets (type breakdown, people & groups, pins,
// activity), framed to match the DetailsPanel chrome so the slide swap between
// dashboard and document details is seamless. The shared shell primitives live
// in ./dashboard/shell so the hub dashboard reuses them; the hub landing pane
// itself now lives in ./hub/HubDashboard (Overview + Explore modes) and is
// re-exported here so BrowserStage's import path is unchanged.
export { HubDashboard } from './hub/HubDashboard'

// ── project dashboard ──────────────────────────────────────────────
// `active` follows the same contract as the other project tabs (see
// ProjectPanel): every tab stays mounted, so without this the dashboard kept
// spending APS quota — the permissions path, the classify fan-out and the
// rollup activity query all running from a tab nobody was looking at.
//
// It gates fetching only, never rendering. The panes cross-slide on tab change,
// so the dashboard has to keep drawing itself on the way out or it would blank
// mid-transition. That is also why its charts are safe to render while hidden:
// a slid pane is inset:0 of a sized slot, so recharts always measures real
// dimensions — the 0x0 box that made it log "width(0) and height(0)" was an
// artefact of the `display: none` this replaced.
export function ProjectDashboard({ active = true }: { active?: boolean }) {
  const { t } = useTranslation('details')
  const nav = useNav()
  const project = nav.project
  const goTo = useGoToDocument()

  // The dashboard reflects the current CONTAINER: the project root, or — once you
  // drill in — the selected folder. Contents come from the project-contents
  // endpoint at root and the folder-contents endpoint inside a folder.
  const atRoot = nav.folderStack.length === 0
  const rootQ = useProjectContents(atRoot ? (project?.id ?? null) : null)
  const folderQ = useFolderContents(nav.hubId, atRoot ? null : nav.currentFolderId)
  const contentsLoading = atRoot ? rootQ.isLoading : folderQ.isLoading
  const items = atRoot ? (rootQ.data?.items ?? []) : (folderQ.data ?? []).filter((i) => !i.isContainer)

  const folder = nav.folderStack[nav.folderStack.length - 1]
  const title = folder?.name ?? project?.name ?? t('dashboards.project')

  // People & groups: effective access at the current container — the deepest
  // layer of the permissions path (the project at root, the folder once inside).
  const folderPath = nav.folderStack.map((f) => ({ id: f.id, name: f.name }))
  const permQ = usePermissionsPath(nav.hubId, project?.id, project?.name, folderPath, !!project?.id && active)
  const currentLayer = permQ.data?.[permQ.data.length - 1]

  // Pins: all of the project's pins at the root, narrowed to the current folder
  // once you drill in. Skip any pin that points at the container currently
  // shown (the project at root, the drilled-in folder otherwise) — a
  // self-referencing pin would just navigate to where you already are.
  const pinsQ = usePins(nav.hubId)
  const containerId = nav.currentFolderId ?? project?.id ?? null
  const containerPins = (pinsQ.data ?? []).filter(
    (p) =>
      p.project_id === project?.id &&
      p.id !== containerId &&
      (atRoot || pinContainerId(p) === nav.currentFolderId),
  )

  const designs = items.filter((i) => (i.kind === 'design' || i.kind === 'configured') && i.componentVersionId)

  // Lifts both fan-out caps below. Declared here because the classify cap needs
  // it; reset when the container changes, since this pane stays mounted.
  const [loadAll, setLoadAll] = useState(false)
  useEffect(() => setLoadAll(false), [project?.id, nav.currentFolderId])

  // Classify the container's designs so the donut can split them into assemblies
  // vs parts, sharing the ['classify', cvId] cache the Contents column fills.
  //
  // Capped like the activity roll-up below, and for the same reason: the column
  // now classifies only rows near the viewport, so an uncapped fan-out here
  // would be the burst that change removes — every design in the container at
  // once, against a per-minute quota that answers the tail with 429. Beyond the
  // cap a design counts as "Designs" until its row is scrolled into view, which
  // is the bucket docTypeOf already falls back to.
  const classifyQs = useQueries({
    queries: designs.map((d, i) => ({
      queryKey: ['classify', d.componentVersionId],
      queryFn: () => api.classify(d.componentVersionId!),
      staleTime: Infinity,
      // Subscribe to every design but only FETCH within the cap: past it the
      // query stays disabled, so a classification the Contents column has
      // already fetched still lands in the donut without costing a request.
      enabled: active && (loadAll || i < CLASSIFY_CAP),
    })),
  })
  const subtypeByCv = new Map<string, string>()
  designs.forEach((d, i) => {
    const sub = classifyQs[i]?.data?.subtype
    if (sub) subtypeByCv.set(d.componentVersionId!, sub)
  })

  // Document-type breakdown for the donut: assemblies/parts (classified),
  // drawings, and uploaded files by extension (PDF / Text / Images / Video / …).
  const typeCounts = (() => {
    const map = new Map<string, number>()
    for (const it of items) {
      const ty = docTypeOf(t, it, it.componentVersionId ? subtypeByCv.get(it.componentVersionId) : undefined)
      map.set(ty, (map.get(ty) ?? 0) + 1)
    }
    return [...map.entries()].map(([label, value]) => ({ label, value })).sort((a, b) => b.value - a.value)
  })()

  // Aggregate activity across the project's root design documents, merged
  // server-side via the roll-up endpoint (one request). Capped by default so a
  // large project doesn't fan out hundreds of GraphQL activity queries (and brush
  // the APS per-minute quota); "Load all" lifts the cap on demand.
  const designIds = designs.map((d) => d.id)
  const activityCapped = !loadAll && designIds.length > ACTIVITY_CAP
  const classifyCapped = !loadAll && designs.length > CLASSIFY_CAP
  // One control lifts both fan-outs, so the notice shows if either is capped —
  // a cap nobody can see or undo is just silent truncation.
  const capped = activityCapped || classifyCapped
  const activityIds = activityCapped ? designIds.slice(0, ACTIVITY_CAP) : designIds
  const rollupQ = useRollupActivity(
    nav.hubId,
    activityIds[0] ?? null,
    activityIds.slice(1),
    activityIds.length > 0 && active,
  )

  return (
    <DashboardShell title={title} subtitle={t('dashboards.projectSubtitle')} fill>
      <Stack spacing={1.5} sx={{ flex: 1, minHeight: 0 }}>
        {/* The three small widgets get their own grid (no full-width siblings)
            so auto-fit collapses any empty track and they fill the row evenly
            (≈33% each) when they share it, wrapping to 2 / 1 as it narrows. */}
        <Box
          sx={{
            display: 'grid',
            gap: 1.5,
            gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
            flexShrink: 0,
          }}
        >
          <WidgetCard title={t('dashboards.documentTypes')}>
            {contentsLoading ? <Hint>{t('common:loading')}</Hint> : <TypeDonut data={typeCounts} />}
          </WidgetCard>
          <WidgetCard title={t('dashboards.peopleGroups')}>
            <PeopleGroups
              members={currentLayer?.members ?? []}
              groups={currentLayer?.groups ?? []}
              loading={permQ.isLoading}
              error={!!permQ.error}
            />
          </WidgetCard>
          <WidgetCard
            title={
              containerPins.length
                ? t('dashboards.pinsWithCount', { count: containerPins.length })
                : t('dashboards.pins')
            }
          >
            <PinsList pins={containerPins} loading={pinsQ.isLoading} onOpen={(p) => goTo({ itemId: p.id, name: p.name, kind: p.kind })} />
          </WidgetCard>
        </Box>
        <WidgetCard title={t('dashboards.projectActivity')} fill>
          {designIds.length === 0 ? (
            <Hint>{t('dashboards.noDesignDocuments')}</Hint>
          ) : (
            <>
              {capped && (
                <Stack direction="row" spacing={1.5} alignItems="center" sx={{ mb: 1, flexShrink: 0 }}>
                  <Typography variant="caption" color="text.secondary">
                    {activityCapped
                      ? t('dashboards.aggregatingFirst', { cap: ACTIVITY_CAP, total: designIds.length })
                      : t('dashboards.aggregatingAll', { count: designIds.length })}
                    {classifyCapped && (
                      <> {t('dashboards.notYetClassified', { count: designs.length - CLASSIFY_CAP })}</>
                    )}
                  </Typography>
                  <Button size="small" onClick={() => setLoadAll(true)} disabled={rollupQ.isFetching}>
                    {rollupQ.isFetching ? t('common:loading') : t('dashboards.loadAll')}
                  </Button>
                </Stack>
              )}
              {rollupQ.isFetching && !rollupQ.data ? (
                <Hint>{t('dashboards.aggregatingActivity')}</Hint>
              ) : rollupQ.error ? (
                <Hint>{t('dashboards.activityUnavailable')}</Hint>
              ) : rollupQ.data ? (
                // Fill the card's leftover height so the heatmap (height:100%)
                // uses the pane, matching the document Activity tab.
                <Box sx={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
                  <ActivityHeatmap report={rollupQ.data} />
                </Box>
              ) : (
                <Hint>{t('dashboards.noActivityRecorded')}</Hint>
              )}
            </>
          )}
        </WidgetCard>
      </Stack>
    </DashboardShell>
  )
}

// ── widgets ────────────────────────────────────────────────────────
// Max root designs aggregated into the project activity chart before "Load all".
const ACTIVITY_CAP = 50

// Max designs the donut classifies up-front before "Load all". One classify is
// one GraphQL call against a per-minute quota, so this bounds what opening a
// large folder costs; the rest resolve as their rows scroll into view.
const CLASSIFY_CAP = 24

function extOf(name: string): string {
  const m = /\.([a-z0-9]+)$/i.exec(name)
  return m ? m[1].toLowerCase() : ''
}

// docTypeOf maps an item to a document-type bucket for the donut: designs split
// into Assemblies/Parts (from the classify subtype, "Designs" until known),
// drawings, electronics, and uploaded files by extension.
function docTypeOf(t: TFunction, item: Item, subtype: string | undefined): string {
  if (item.kind === 'folder') return t('dashboards.docType.folders')
  if (item.kind === 'design' || item.kind === 'configured') {
    const st = subtype || item.subtype
    return st === 'assembly'
      ? t('dashboards.docType.assemblies')
      : st === 'part'
        ? t('dashboards.docType.parts')
        : t('dashboards.docType.designs')
  }
  if (item.kind === 'drawing')
    return item.subtype === 'template' ? t('dashboards.docType.templates') : t('dashboards.docType.drawings')
  if (item.kind === 'schematic') return t('dashboards.docType.schematics')
  if (item.kind === 'pcb') return t('dashboards.docType.pcb')
  if (item.kind === 'ecad') return t('dashboards.docType.ecad')
  const ext = extOf(item.name)
  if (ext === 'pdf') return t('dashboards.docType.pdf')
  if (['txt', 'md', 'csv', 'json', 'xml', 'log', 'rtf'].includes(ext)) return t('dashboards.docType.text')
  if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'bmp', 'tif', 'tiff', 'heic'].includes(ext))
    return t('dashboards.docType.images')
  if (['mp4', 'mov', 'avi', 'webm', 'mkv', 'm4v'].includes(ext)) return t('dashboards.docType.video')
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return t('dashboards.docType.archives')
  return ext ? ext.toUpperCase() : t('dashboards.docType.other')
}

function TypeDonut({ data }: { data: Array<{ label: string; value: number }> }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const total = data.reduce((s, d) => s + d.value, 0)
  if (total === 0) return <Hint>{t('dashboards.emptyProject')}</Hint>
  return (
    <Box sx={{ display: 'flex', gap: 2, alignItems: 'center', flexWrap: 'wrap' }}>
      <Box sx={{ position: 'relative', width: 150, height: 150, flexShrink: 0 }}>
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie data={data} dataKey="value" nameKey="label" innerRadius={48} outerRadius={72} paddingAngle={2} stroke="none">
              {data.map((d, i) => (
                <Cell key={d.label} fill={DONUT_PALETTE[i % DONUT_PALETTE.length]} />
              ))}
            </Pie>
            <RechartsTooltip
              contentStyle={{
                background: theme.palette.background.paper,
                border: `1px solid ${theme.palette.divider}`,
                borderRadius: 6,
                fontSize: 12,
              }}
            />
          </PieChart>
        </ResponsiveContainer>
        <Box
          sx={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            pointerEvents: 'none',
          }}
        >
          <Typography variant="h5" fontWeight={700} lineHeight={1}>
            {total}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            {t('dashboards.items')}
          </Typography>
        </Box>
      </Box>
      <Stack spacing={0.5} sx={{ flex: 1, minWidth: 140 }}>
        {data.map((d, i) => (
          <Box key={d.label} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Box sx={{ width: 10, height: 10, borderRadius: '2px', bgcolor: DONUT_PALETTE[i % DONUT_PALETTE.length], flexShrink: 0 }} />
            <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }}>
              {d.label}
            </Typography>
            <Typography variant="body2" fontWeight={600}>
              {d.value}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ width: 38, textAlign: 'right' }}>
              {Math.round((d.value / total) * 100)}%
            </Typography>
          </Box>
        ))}
      </Stack>
    </Box>
  )
}

function RoleChip({ role }: { role: string }) {
  const { t } = useTranslation('details')
  if (!role) return null
  // roleLabel falls back to the raw uppercased token when the role is missing
  // from the enums catalog; keep the old lowercase+capitalize rendering there.
  const translated = roleLabel(t, role)
  return (
    <Chip
      size="small"
      label={translated === role.toUpperCase() ? role.toLowerCase() : translated}
      sx={{ height: 18, fontSize: 10, textTransform: 'capitalize', flexShrink: 0 }}
    />
  )
}

function PeopleGroups({
  members,
  groups,
  loading,
  error,
}: {
  members: PermMember[]
  groups: ProjectGroup[]
  loading: boolean
  error: boolean
}) {
  const { t } = useTranslation('details')
  if (loading) return <Hint>{t('common:loading')}</Hint>
  if (error) return <Hint>{t('dashboards.accessUnavailable')}</Hint>
  if (members.length === 0 && groups.length === 0) return <Hint>{t('dashboards.noPeopleOrGroups')}</Hint>
  const shownMembers = members.slice(0, 8)
  return (
    <Stack spacing={1.5}>
      {groups.length > 0 && (
        <Box>
          <Typography variant="caption" color="text.secondary">
            {t('dashboards.groupCount', { count: groups.length })}
          </Typography>
          <Stack spacing={0.5} sx={{ mt: 0.5 }}>
            {groups.map((g) => (
              <Box key={g.id} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <FontAwesomeIcon icon={faUsers} style={{ fontSize: 12, opacity: 0.7, width: 22 }} />
                <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }}>
                  {g.name}
                </Typography>
                <RoleChip role={g.role} />
              </Box>
            ))}
          </Stack>
        </Box>
      )}
      {members.length > 0 && (
        <Box>
          <Typography variant="caption" color="text.secondary">
            {t('dashboards.memberCount', { count: members.length })}
          </Typography>
          <Stack spacing={0.5} sx={{ mt: 0.5 }}>
            {shownMembers.map((m) => (
              <Box key={m.userId} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <UserAvatar id={m.userId} name={m.name} size={22} />
                <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }} title={m.email}>
                  {m.name}
                </Typography>
                <RoleChip role={m.role} />
              </Box>
            ))}
            {members.length > shownMembers.length && (
              <Typography variant="caption" color="text.secondary" sx={{ pl: 3.5 }}>
                {t('dashboards.moreMembers', { count: members.length - shownMembers.length })}
              </Typography>
            )}
          </Stack>
        </Box>
      )}
    </Stack>
  )
}

// pinContainerId is the id of the folder a pin lives directly in (null at the
// project root). Document pins store their parent path in folder_path; folder
// pins append themselves, so drop the last entry for those.
function pinContainerId(p: Pin): string | null {
  const fp = p.folder_path ?? []
  const parent = p.kind === 'folder' ? fp.slice(0, -1) : fp
  return parent.length ? parent[parent.length - 1].id : null
}

function PinsList({ pins, loading, onOpen }: { pins: Pin[]; loading: boolean; onOpen: (p: Pin) => void }) {
  const { t } = useTranslation('details')
  if (loading) return <Hint>{t('common:loading')}</Hint>
  if (pins.length === 0) return <Hint>{t('dashboards.noPinsHere')}</Hint>
  return (
    <List dense disablePadding>
      {pins.map((p) => (
        <ListItemButton key={p.id} onClick={() => onOpen(p)} sx={{ borderRadius: 1, py: 0.25, gap: 1 }}>
          <ItemIcon item={{ kind: p.kind }} style={{ fontSize: 13, width: 18, opacity: 0.7, flexShrink: 0 }} />
          <Typography variant="body2" noWrap sx={{ flex: 1, minWidth: 0 }}>
            {p.name}
          </Typography>
        </ListItemButton>
      ))}
    </List>
  )
}
