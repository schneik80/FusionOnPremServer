import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  Box,
  Chip,
  CircularProgress,
  InputBase,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha, useTheme, type Theme } from '@mui/material/styles'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faChevronRight,
  faCube,
  faFile,
  faFolder,
  faLayerGroup,
  faMagnifyingGlass,
  faSitemap,
  faUsers,
} from '@fortawesome/free-solid-svg-icons'
import { usePermissionsPath } from '../api/queries'
import { roleLabel } from '../i18n/enums'
import { useNav } from '../state/nav'
import type { Item, PermLayer } from '../api/types'
import { firstGrapheme, truncateGraphemes } from '../fmt/graphemes'

// PermissionsExplorer adapts the "Permission Explorer" prototype to the real
// Fusion model. Each layer of a document's path — the project, then each folder —
// reports its EFFECTIVE access (folder.members / project members include inherited
// grants). So the leaf folder's list is exactly who can reach the document, and
// comparing layers reveals where a role is directly applied, raised, lowered, or
// a member is denied ("No role" — present above, absent below). See memory
// mdm-permission-model.

const ROLE_RANK: Record<string, number> = {
  viewer: 1,
  reader: 2,
  editor: 3,
  contributor: 3,
  manager: 4,
  administrator: 5,
  admin: 5,
  project_admin: 5,
  owner: 5,
}
type RoleInfo = { rank: number; intensity: number; label: string }
function roleInfo(t: TFunction, role: string): RoleInfo {
  const rank = ROLE_RANK[(role || '').toLowerCase()] ?? 1
  // roleLabel resolves known role tokens through the enums catalog; it falls
  // back to the raw uppercased token for roles the catalog doesn't carry
  // (reader/contributor/owner/…), where the old prettifier still applies.
  let label: string
  if (!role) {
    label = t('details:permissions.member')
  } else {
    const translated = roleLabel(t, role)
    label =
      translated === role.toUpperCase()
        ? role.toLowerCase().replace(/\b\w/g, (c) => c.toUpperCase()).replace(/_/g, ' ')
        : translated
  }
  return {
    rank,
    intensity: [0, 0.34, 0.5, 0.68, 0.84, 1][rank] ?? 0.34,
    label,
  }
}

type Kind = 'granted' | 'inherit' | 'raised' | 'lowered' | 'denied' | 'absent'
type Principal = {
  kind: 'group' | 'user'
  id: string
  name: string
  status?: string
  seq: (string | null)[] // effective role per layer (null = no access there)
  kinds: Kind[]
  leafRole: string | null // effective role at the document
  originIdx: number // layer where the leaf role was directly applied / changed
  denyIdx: number // first layer where access was removed
  eff: RoleInfo
}

const roleAt = (layer: PermLayer, kind: 'group' | 'user', id: string): string | null =>
  (kind === 'group' ? layer.groups.find((g) => g.id === id)?.role : layer.members.find((m) => m.userId === id)?.role) ??
  null

// resolve a principal's effective access down the path: classify each layer and
// find the leaf (document) role plus where it originates / is denied.
function resolve(layers: PermLayer[], kind: 'group' | 'user', id: string) {
  const seq = layers.map((l) => roleAt(l, kind, id))
  const kinds: Kind[] = []
  let prev: string | null = null
  let originIdx = -1
  let denyIdx = -1
  seq.forEach((role, i) => {
    let k: Kind
    if (role != null && prev == null) k = 'granted'
    else if (role != null && prev != null && role === prev) k = 'inherit'
    else if (role != null && prev != null) k = ROLE_RANK[role.toLowerCase()] > ROLE_RANK[prev.toLowerCase()] ? 'raised' : 'lowered'
    else if (role == null && prev != null) k = 'denied'
    else k = 'absent'
    if (k === 'granted' || k === 'raised' || k === 'lowered') originIdx = i
    if (k === 'denied' && denyIdx < 0) denyIdx = i
    kinds.push(k)
    prev = role
  })
  return { seq, kinds, leafRole: seq[seq.length - 1] ?? null, originIdx, denyIdx }
}

const initials = (name: string) =>
  name.split(/\s+/).map((w) => firstGrapheme(w)).slice(0, 2).join('').toUpperCase()
const truncate = (s: string, n: number) => truncateGraphemes(s, n)

export default function PermissionsExplorer({ hubId, item }: { hubId: string | null; item: Item }) {
  const { t } = useTranslation('details')
  const theme = useTheme()
  const nav = useNav()
  const projectId = nav.project?.id ?? null

  const folders = useMemo(() => nav.folderStack.map((f) => ({ id: f.id, name: f.name })), [nav.folderStack])
  const q = usePermissionsPath(hubId, projectId, nav.project?.name, folders, !!projectId)
  const layers = useMemo(() => q.data ?? [], [q.data])

  const { access, denied } = useMemo(() => {
    const groupIds = new Map<string, string>()
    const userMeta = new Map<string, { name: string; status?: string }>()
    for (const l of layers) {
      for (const g of l.groups) groupIds.set(g.id, g.name)
      for (const m of l.members) userMeta.set(m.userId, { name: m.name, status: m.status })
    }
    const all: Principal[] = []
    for (const [id, name] of groupIds) {
      const r = resolve(layers, 'group', id)
      all.push({ kind: 'group', id, name, ...r, eff: roleInfo(t, r.leafRole ?? '') })
    }
    for (const [id, meta] of userMeta) {
      const r = resolve(layers, 'user', id)
      all.push({ kind: 'user', id, name: meta.name, status: meta.status, ...r, eff: roleInfo(t, r.leafRole ?? '') })
    }
    const access = all.filter((p) => p.leafRole != null).sort((a, b) => b.eff.rank - a.eff.rank || (a.kind === b.kind ? 0 : a.kind === 'group' ? -1 : 1))
    const denied = all.filter((p) => p.leafRole == null && p.denyIdx >= 0)
    return { access, denied }
  }, [layers, t])

  const [tab, setTab] = useState<'all' | 'group' | 'user'>('all')
  const [query, setQuery] = useState('')
  const [active, setActive] = useState<string | null>(null)
  const [hover, setHover] = useState<string | null>(null)
  const [viz, setViz] = useState<'circles' | 'layers'>('layers')
  const activeP = [...access, ...denied].find((p) => p.id === (hover ?? active)) ?? null

  const matches = (p: Principal) =>
    (tab === 'all' || p.kind === tab) && (!query.trim() || p.name.toLowerCase().includes(query.trim().toLowerCase()))

  if (!projectId) return <Empty text={t('permissions.selectDocument')} />
  if (q.isLoading) return <Spinner />
  if (q.error) return <Empty text={(q.error as Error).message} />
  if (layers.length === 0) return <Empty text={t('permissions.noAccessInfo')} />

  // Name a path layer by its object type *and* name, e.g. "Project (Stream
  // Cheap)" / "Folder (Manufacturing)", so a row reads "Directly applied to
  // Project (…)" rather than a bare name that hides whether it's a project or
  // folder. Falls back to just the type word when the name is unavailable.
  const layerName = (i: number) => {
    const l = layers[i]
    const kind = l?.type === 'project' ? t('permissions.layerProject') : t('permissions.layerFolder')
    return l?.name ? t('permissions.layerWithName', { kind, name: l.name }) : kind
  }
  const fAccess = access.filter(matches)
  const fDenied = denied.filter(matches)
  const groups = fAccess.filter((p) => p.kind === 'group')
  const users = fAccess.filter((p) => p.kind === 'user')

  return (
    <Stack spacing={2}>
      {/* ── Path layers ─────────────────────────────────────────── */}
      <Box>
        <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 0.5 }}>
          <Head icon={faSitemap} title={t('permissions.pathLayers')} sub={t('permissions.layerCount', { count: layers.length })} />
          <ToggleButtonGroup size="small" exclusive value={viz} onChange={(_, v: typeof viz | null) => v && setViz(v)}>
            <ToggleButton value="layers" sx={{ py: 0, px: 1, fontSize: 11 }}>
              {t('permissions.vizLayers')}
            </ToggleButton>
            <ToggleButton value="circles" sx={{ py: 0, px: 1, fontSize: 11 }}>
              {t('permissions.vizCircles')}
            </ToggleButton>
          </ToggleButtonGroup>
        </Stack>
        {viz === 'circles' ? (
          <Rings layers={layers} item={item} active={activeP} theme={theme} />
        ) : (
          <LayersViz layers={layers} item={item} active={activeP} theme={theme} />
        )}
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', textAlign: 'center', mt: 0.5 }}>
          {activeP ? calloutText(t, activeP, layerName, item.kind) : t('permissions.hoverHint', { kind: item.kind })}
        </Typography>
      </Box>

      {/* ── With access ─────────────────────────────────────────── */}
      <Box>
        <Head icon={faUsers} title={t('permissions.withAccess')} sub={t('permissions.accessSummary', { access: access.length, denied: denied.length })} />

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, px: 1, height: 34, mb: 1, border: 1, borderColor: 'divider', borderRadius: 1 }}>
          <FontAwesomeIcon icon={faMagnifyingGlass} style={{ fontSize: 12, opacity: 0.6 }} />
          <InputBase placeholder={t('permissions.searchPlaceholder')} value={query} onChange={(e) => setQuery(e.target.value)} sx={{ flex: 1, fontSize: 13 }} />
        </Box>
        <ToggleButtonGroup size="small" exclusive value={tab} onChange={(_, v: typeof tab | null) => v && setTab(v)} fullWidth sx={{ mb: 1 }}>
          <ToggleButton value="all">{t('permissions.filterAll')}</ToggleButton>
          <ToggleButton value="group">{t('permissions.filterGroups')}</ToggleButton>
          <ToggleButton value="user">{t('permissions.filterPeople')}</ToggleButton>
        </ToggleButtonGroup>

        <Stack spacing={0.5}>
          {(tab === 'all' || tab === 'group') && groups.length > 0 && <Label text={t('permissions.groups')} n={groups.length} />}
          {groups.map((p) => (
            <Row key={p.id} p={p} layerName={layerName} activeId={hover ?? active} onHover={setHover} onClick={() => setActive((a) => (a === p.id ? null : p.id))} theme={theme} />
          ))}
          {(tab === 'all' || tab === 'user') && users.length > 0 && <Label text={t('permissions.people')} n={users.length} />}
          {users.map((p) => (
            <Row key={p.id} p={p} layerName={layerName} activeId={hover ?? active} onHover={setHover} onClick={() => setActive((a) => (a === p.id ? null : p.id))} theme={theme} />
          ))}
          {fDenied.length > 0 && (
            <>
              <Label text={t('permissions.deniedHere')} n={fDenied.length} />
              {fDenied.map((p) => (
                <Row key={p.id} p={p} layerName={layerName} activeId={hover ?? active} onHover={setHover} onClick={() => setActive((a) => (a === p.id ? null : p.id))} theme={theme} denied />
              ))}
            </>
          )}
          {fAccess.length === 0 && fDenied.length === 0 && (
            <Typography variant="caption" color="text.secondary" sx={{ py: 2, textAlign: 'center' }}>
              {t('permissions.noMatches')}
            </Typography>
          )}
        </Stack>
      </Box>
    </Stack>
  )
}

function calloutText(t: TFunction, p: Principal, layerName: (i: number) => string, itemKind: string): string {
  if (p.leafRole == null)
    return t('details:permissions.calloutDenied', { name: p.name, layer: layerName(p.denyIdx) })
  const k = p.kinds[p.originIdx]
  const atLeaf = p.originIdx === p.seq.length - 1
  const key =
    k === 'raised'
      ? atLeaf
        ? 'details:permissions.calloutRaised'
        : 'details:permissions.calloutRaisedInherited'
      : k === 'lowered'
        ? atLeaf
          ? 'details:permissions.calloutLowered'
          : 'details:permissions.calloutLoweredInherited'
        : atLeaf
          ? 'details:permissions.calloutDirect'
          : 'details:permissions.calloutInherited'
  return t(key, { name: p.name, role: p.eff.label, layer: layerName(p.originIdx), kind: itemKind })
}

// ── rings ─────────────────────────────────────────────────────────
function Rings({ layers, item, active, theme }: { layers: PermLayer[]; item: Item; active: Principal | null; theme: Theme }) {
  const { t } = useTranslation('details')
  const accent = theme.palette.primary.main
  const muted = theme.palette.text.secondary
  const track = alpha(theme.palette.text.primary, theme.palette.mode === 'dark' ? 0.16 : 0.12)
  const nodes = [...layers.map((l) => ({ type: l.type, name: l.name || l.type })), { type: item.kind, name: item.name }]
  const SIZE = 260
  const C = SIZE / 2
  const DISC = 34
  const last = nodes.length - 1
  const rings = last
  const gap = rings > 0 ? (SIZE / 2 - DISC - 16) / (rings + 0.5) : 0
  const RT = Math.max(6, gap * 0.7)
  const ringR = (s: number) => DISC + gap * 0.6 + s * gap
  const PAD = 26 // room for the outer ring stroke + 12-o'clock markers/labels

  const leaf = active?.leafRole ?? null
  const discInt = leaf ? roleInfo(t, leaf).intensity : 0
  const discLabel = active ? (leaf ? roleInfo(t, leaf).label : t('permissions.noAccess')) : '—'

  return (
    <Box sx={{ display: 'flex', justifyContent: 'center', py: 1 }}>
      <svg
        width={SIZE}
        height={SIZE}
        viewBox={`${-PAD} ${-PAD} ${SIZE + PAD * 2} ${SIZE + PAD * 2}`}
        role="img"
        aria-label={t('permissions.pathLayersAria')}
      >
        <circle cx={C} cy={C} r={DISC} fill={discInt ? alpha(accent, discInt * 0.7) : 'transparent'} stroke={active && !leaf ? muted : track} strokeWidth={1.5} strokeDasharray={active && !leaf ? '3 3' : undefined} />
        <text x={C} y={C - 8} textAnchor="middle" fontSize={8} fill={muted} style={{ letterSpacing: 1 }}>
          {(nodes[last].type || '').toUpperCase()}
        </text>
        <text x={C} y={C + 5} textAnchor="middle" fontSize={12} fontWeight={600} fill={active && !leaf ? muted : theme.palette.text.primary}>
          {discLabel}
        </text>
        <text x={C} y={C + 18} textAnchor="middle" fontSize={8} fill={muted}>
          {truncate(nodes[last].name, 16)}
        </text>
        {Array.from({ length: rings }, (_, k) => {
          const s = k + 1
          const ni = last - s
          const r = ringR(s)
          const role = active ? active.seq[ni] : null
          const kind = active ? active.kinds[ni] : 'absent'
          const set = kind === 'granted' || kind === 'raised' || kind === 'lowered'
          return (
            <g key={s}>
              <circle cx={C} cy={C} r={r} fill="none" stroke={track} strokeWidth={RT} />
              {role != null && <circle cx={C} cy={C} r={r} fill="none" stroke={accent} strokeWidth={RT} strokeOpacity={roleInfo(t, role).intensity} />}
              {kind === 'denied' && <circle cx={C} cy={C} r={r} fill="none" stroke={muted} strokeWidth={RT} strokeOpacity={0.5} strokeDasharray="2 6" />}
              {/* marker on the ring at 12 o'clock */}
              {set && <circle cx={C} cy={C - r} r={5} fill={kind === 'lowered' ? theme.palette.background.paper : accent} stroke={accent} strokeWidth={1.5} />}
              {kind === 'denied' && (
                <g>
                  <circle cx={C} cy={C - r} r={5} fill={theme.palette.background.paper} stroke={muted} strokeWidth={1.3} />
                  <path d={`M ${C - 3} ${C - r + 3} L ${C + 3} ${C - r - 3}`} stroke={muted} strokeWidth={1.3} />
                </g>
              )}
              <text x={C} y={C - r} dy={set || kind === 'denied' ? -10 : 3} textAnchor="middle" fontSize={8} fontWeight={set ? 700 : 500} fill={set ? accent : muted}>
                {set
                  ? kind === 'granted'
                    ? t('permissions.markSet')
                    : kind === 'raised'
                      ? '▲'
                      : '▼'
                  : kind === 'denied'
                    ? t('permissions.markDeny')
                    : nodes[ni].type === 'project'
                      ? t('permissions.markProject')
                      : t('permissions.markInherited')}
              </text>
            </g>
          )
        })}
      </svg>
    </Box>
  )
}

const layerIcon = (type: string) =>
  type === 'hub'
    ? faSitemap
    : type === 'project'
      ? faLayerGroup
      : type === 'folder'
        ? faFolder
        : type === 'design' || type === 'configured'
          ? faCube
          : faFile
// Catalog keys per layer kind (details namespace); rendered through t() below.
const KIND_LABEL: Record<Kind | 'selected', string> = {
  granted: 'permissions.kindSet',
  inherit: 'permissions.kindInherited',
  raised: 'permissions.kindRaised',
  lowered: 'permissions.kindLowered',
  denied: 'permissions.kindDenied',
  absent: 'permissions.kindAbsent',
  selected: 'permissions.kindSelected',
}

// ── layers viz (the prototype's horizontal path spine) ────────────
function LayersViz({ layers, item, active, theme }: { layers: PermLayer[]; item: Item; active: Principal | null; theme: Theme }) {
  const { t } = useTranslation('details')
  const accent = theme.palette.primary.main
  const muted = theme.palette.text.secondary
  const nodes = [
    ...layers.map((l, i) => ({
      type: l.type,
      idx: i === 0 ? 'ROOT' : `L${i}`,
      name: l.name || l.type,
      role: active ? active.seq[i] : null,
      kind: (active ? active.kinds[i] : 'absent') as Kind | 'selected',
    })),
    { type: item.kind, idx: 'DOC', name: item.name, role: active ? active.leafRole : null, kind: 'selected' as const },
  ]
  return (
    <Box sx={{ overflowX: 'auto', py: 1 }}>
      <Stack direction="row" alignItems="stretch" sx={{ minWidth: 'min-content' }}>
        {nodes.map((n, i) => {
          const deny = n.kind === 'denied'
          const ri = n.role ? roleInfo(t, n.role) : null
          const hot = n.kind === 'granted' || n.kind === 'raised' || n.kind === 'lowered' || deny
          return (
            <Box key={i} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box
                sx={{
                  width: 100,
                  p: 1,
                  border: 1,
                  borderRadius: 1,
                  borderColor: hot ? alpha(accent, 0.4) : 'divider',
                  bgcolor: hot ? alpha(accent, 0.08) : 'transparent',
                }}
              >
                <Stack direction="row" spacing={0.5} alignItems="center" sx={{ color: 'text.secondary', mb: 0.25 }}>
                  <FontAwesomeIcon icon={layerIcon(n.type)} style={{ fontSize: 11 }} />
                  <Typography variant="caption" sx={{ fontFamily: 'monospace' }}>
                    {n.idx}
                  </Typography>
                </Stack>
                <Typography variant="body2" fontWeight={600} noWrap title={n.name}>
                  {n.name}
                </Typography>
                <Box sx={{ height: 5, borderRadius: 999, bgcolor: 'divider', overflow: 'hidden', my: 0.5 }}>
                  <Box
                    sx={{
                      height: '100%',
                      width: deny ? '100%' : ri ? `${Math.round(ri.intensity * 100)}%` : '0%',
                      bgcolor: deny ? muted : accent,
                      opacity: deny ? 0.5 : ri ? 0.45 + ri.intensity * 0.55 : 1,
                      borderRadius: 999,
                    }}
                  />
                </Box>
                <Typography variant="caption" color={deny ? 'text.secondary' : 'text.secondary'} sx={{ fontSize: 10 }}>
                  {t(KIND_LABEL[n.kind])}
                </Typography>
              </Box>
              {i < nodes.length - 1 && (
                <FontAwesomeIcon icon={faChevronRight} style={{ fontSize: 10, opacity: 0.4, margin: '0 3px' }} />
              )}
            </Box>
          )
        })}
      </Stack>
    </Box>
  )
}

// ── a principal row ───────────────────────────────────────────────
function Row({
  p,
  layerName,
  activeId,
  onHover,
  onClick,
  theme,
  denied,
}: {
  p: Principal
  layerName: (i: number) => string
  activeId: string | null
  onHover: (id: string | null) => void
  onClick: () => void
  theme: Theme
  denied?: boolean
}) {
  const { t } = useTranslation('details')
  const accent = theme.palette.primary.main
  const isActive = activeId === p.id
  let sub: string
  if (denied) {
    sub = t('permissions.noRoleOn', { layer: layerName(p.denyIdx) })
  } else {
    const k = p.kinds[p.originIdx]
    const where = layerName(p.originIdx)
    sub =
      p.originIdx === p.seq.length - 1
        ? k === 'raised'
          ? t('permissions.raisedOn', { layer: where })
          : k === 'lowered'
            ? t('permissions.loweredOn', { layer: where })
            : t('permissions.directlyAppliedTo', { layer: where })
        : t('permissions.inheritedFrom', { layer: where })
  }

  return (
    <Box
      onMouseEnter={() => onHover(p.id)}
      onMouseLeave={() => onHover(null)}
      onClick={onClick}
      sx={{
        display: 'grid',
        gridTemplateColumns: '28px 1fr auto',
        alignItems: 'center',
        gap: 1,
        px: 1,
        py: 0.75,
        borderRadius: 1,
        border: 1,
        borderColor: isActive ? alpha(accent, 0.45) : 'transparent',
        bgcolor: isActive ? alpha(accent, 0.12) : 'transparent',
        cursor: 'pointer',
        opacity: denied ? 0.7 : 1,
        '&:hover': { bgcolor: alpha(accent, 0.08) },
      }}
    >
      <Box
        sx={{
          width: 28,
          height: 28,
          borderRadius: '50%',
          display: 'grid',
          placeItems: 'center',
          fontSize: 11,
          fontWeight: 700,
          border: 1,
          borderColor: 'divider',
          bgcolor: p.kind === 'group' ? 'transparent' : alpha(accent, denied ? 0.06 : 0.16),
          color: p.kind === 'group' ? 'text.secondary' : denied ? 'text.secondary' : accent,
        }}
      >
        {p.kind === 'group' ? <FontAwesomeIcon icon={faUsers} style={{ fontSize: 13 }} /> : initials(p.name)}
      </Box>
      <Box sx={{ minWidth: 0 }}>
        <Stack direction="row" alignItems="center" spacing={0.5} sx={{ minWidth: 0 }}>
          <Typography variant="body2" fontWeight={600} noWrap>
            {p.name}
          </Typography>
          {p.status === 'PENDING' && <Chip label={t('permissions.invited')} size="small" variant="outlined" sx={{ height: 16, fontSize: 9 }} />}
        </Stack>
        <Typography variant="caption" color="text.secondary" noWrap sx={{ display: 'block' }}>
          {sub}
        </Typography>
      </Box>
      {denied ? (
        <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75, height: 22, px: 1, borderRadius: 999, border: 1, borderColor: 'divider', fontSize: 11, fontWeight: 600, color: 'text.secondary' }}>
          <Box sx={{ width: 9, height: 9, borderRadius: '50%', border: 1, borderColor: 'text.secondary' }} />
          {t('permissions.noAccess')}
        </Box>
      ) : (
        <RoleBadge info={p.eff} accent={accent} />
      )}
    </Box>
  )
}

function RoleBadge({ info, accent }: { info: RoleInfo; accent: string }) {
  return (
    <Tooltip title={info.label}>
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75, height: 22, px: 1, borderRadius: 999, border: 1, borderColor: 'divider', fontSize: 11, fontWeight: 600, whiteSpace: 'nowrap' }}>
        <Box sx={{ width: 9, height: 9, borderRadius: '50%', bgcolor: accent, opacity: 0.35 + info.intensity * 0.65 }} />
        {info.label}
      </Box>
    </Tooltip>
  )
}

function Head({ icon, title, sub }: { icon: typeof faUsers; title: string; sub: string }) {
  return (
    <Stack direction="row" alignItems="baseline" spacing={1} sx={{ mb: 0.5 }}>
      <FontAwesomeIcon icon={icon} style={{ fontSize: 11, opacity: 0.7 }} />
      <Typography variant="overline" sx={{ fontWeight: 700, letterSpacing: '0.12em', lineHeight: 1 }}>
        {title}
      </Typography>
      <Typography variant="caption" color="text.secondary">
        {sub}
      </Typography>
    </Stack>
  )
}

function Label({ text, n }: { text: string; n: number }) {
  return (
    <Typography variant="caption" color="text.secondary" sx={{ fontWeight: 700, letterSpacing: '0.1em', textTransform: 'uppercase', pt: 1 }}>
      {text} <span style={{ opacity: 0.7 }}>{n}</span>
    </Typography>
  )
}

const Empty = ({ text }: { text: string }) => (
  <Typography variant="body2" color="text.secondary" sx={{ p: 1 }}>
    {text}
  </Typography>
)
const Spinner = () => (
  <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
    <CircularProgress size={24} />
  </Box>
)
