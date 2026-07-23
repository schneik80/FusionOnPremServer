import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faChevronDown,
  faChevronRight,
  faFileImage,
  faMagnifyingGlass,
} from '@fortawesome/free-solid-svg-icons'
import {
  Alert,
  Box,
  Breadcrumbs,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  InputAdornment,
  Link,
  List,
  ListItemButton,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useMemo, useState } from 'react'
import { api } from '../../api/client'
import { useBrowseContents, useProjects } from '../../api/queries'
import type { Item } from '../../api/types'
import { iconForItem, typeTag } from '../icons'
import { ItemIcon } from '../entityIcons'
import { extOf, viewerKindFor } from '../viewers/kind'

// HubBrowserDialog is the shared in-place hub browser: an overlay for picking a
// document (or a folder) anywhere in the hub without leaving the current view.
// Left pane: a lazy-loading tree of projects → folders. Right pane: the
// selected location's contents. It is intentionally generic — callers narrow it
// with `mode` (document vs folder pick) and a `selectable` predicate — so new
// "browse to a document / choose a folder" commands (wiki image embeds today,
// more to come) share one implementation.
//
// Listing runs in Data-Management id space (useBrowseContents): the GraphQL
// contents endpoints the main columns use miss DM-created files and folders —
// notably the wiki's own Wiki/<page>/images/ trees — so folder ids here are DM
// folder urns and item ids are lineage urns (what /api/items/file streams by).

// HubPick describes where a completed browse landed: the project, the folder
// trail from its root, and — in document mode — the chosen document.
export interface HubPick {
  hubId: string
  project: Item
  folderPath: Item[]
  item: Item | null
}

export interface HubBrowserDialogProps {
  open: boolean
  hubId: string | null
  title: string
  /** 'document' picks a file (default); 'folder' picks the current location. */
  mode?: 'document' | 'folder'
  /** document mode: which documents are pickable; the rest render greyed out. */
  selectable?: (item: Item) => boolean
  /** project to open the browser at (e.g. the one the caller is working in). */
  initialProject?: Item | null
  /** confirm-button label, e.g. "Embed image"; defaults per mode. */
  pickLabel?: string
  onClose: () => void
  onPick: (pick: HubPick) => void
}

// isImageDocument is the pickable-predicate for image embeds (shared by any
// caller that wants "an image file from the hub").
export function isImageDocument(item: Item): boolean {
  return !item.isContainer && viewerKindFor(item.name) === 'image'
}

// hubFileSrc turns a document pick into the same-origin URL streaming its tip
// bytes (an <img> src / markdown image target). Null when the pick isn't a
// document or its project carries no data-management id to address it with.
export function hubFileSrc(pick: HubPick): string | null {
  if (!pick.item || !pick.project.altId) return null
  return api.fileUrl(pick.project.altId, pick.item.id, pick.item.name)
}

// docIcon refines the generic file glyph for the picker list: image files show
// as images no matter what the caller is picking.
function docIcon(item: Item) {
  if (!item.isContainer && viewerKindFor(item.name) === 'image') return faFileImage
  return iconForItem(item)
}

export function HubBrowserDialog({
  open,
  hubId,
  title,
  mode = 'document',
  selectable,
  initialProject = null,
  pickLabel,
  onClose,
  onPick,
}: HubBrowserDialogProps) {
  // The current browse location (project + folder trail), the highlighted
  // document, and which tree nodes are unfolded.
  const [project, setProject] = useState<Item | null>(null)
  const [folderStack, setFolderStack] = useState<Item[]>([])
  const [doc, setDoc] = useState<Item | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [filter, setFilter] = useState('')

  // Reset to the caller's starting point each time the dialog opens.
  useEffect(() => {
    if (!open) return
    setProject(initialProject ?? null)
    setFolderStack([])
    setDoc(null)
    setFilter('')
    setExpanded(new Set(initialProject ? [initialProject.id] : []))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // goTo moves the browse location and unfolds the tree path leading to it, so
  // navigating in the right pane keeps the left pane in step.
  function goTo(p: Item, stack: Item[]) {
    setProject(p)
    setFolderStack(stack)
    setDoc(null)
    setExpanded((prev) => {
      const next = new Set(prev)
      next.add(p.id)
      for (const f of stack) next.add(f.id)
      return next
    })
  }

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const canPickDoc = mode === 'document' && !!doc && (selectable ? selectable(doc) : true)
  const canConfirm = mode === 'folder' ? !!project : canPickDoc

  function confirm(item: Item | null) {
    if (!hubId || !project) return
    onPick({ hubId, project, folderPath: folderStack, item })
  }

  const currentId = folderStack.length ? folderStack[folderStack.length - 1].id : project?.id ?? null

  return (
    <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth PaperProps={{ sx: { height: '74vh' } }}>
      <DialogTitle sx={{ pb: 1 }}>{title}</DialogTitle>
      <DialogContent dividers sx={{ p: 0, display: 'flex', minHeight: 0 }}>
        {hubId && (
          <>
            <TreePane
              hubId={hubId}
              filter={filter}
              onFilter={setFilter}
              expanded={expanded}
              onToggle={toggle}
              currentId={currentId}
              onGo={goTo}
            />
            <ContentsPane
              hubId={hubId}
              project={project}
              folderStack={folderStack}
              mode={mode}
              selectable={selectable}
              doc={doc}
              onGo={goTo}
              onSelectDoc={setDoc}
              onPickDoc={(item) => confirm(item)}
            />
          </>
        )}
      </DialogContent>
      <DialogActions sx={{ px: 2, py: 1.5 }}>
        {mode === 'document' && doc && project?.altId && isImageDocument(doc) && (
          <DocPreview key={doc.id} src={api.fileUrl(project.altId, doc.id, doc.name)} />
        )}
        <Typography variant="body2" color="text.secondary" noWrap sx={{ flex: 1, minWidth: 0 }}>
          {mode === 'folder'
            ? project
              ? [project.name, ...folderStack.map((f) => f.name)].join(' / ')
              : 'Select a project or folder.'
            : doc
              ? doc.name
              : 'Select a document.'}
        </Typography>
        <Button onClick={onClose} color="inherit">
          Cancel
        </Button>
        <Button
          variant="contained"
          disabled={!canConfirm}
          onClick={() => confirm(mode === 'folder' ? null : doc)}
        >
          {pickLabel ?? (mode === 'folder' ? 'Select this folder' : 'Select')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

// DocPreview shows a small thumbnail of the highlighted image document in the
// footer; keyed by document id so the failed state resets per document.
function DocPreview({ src }: { src: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return null
  return (
    <Box
      component="img"
      src={src}
      alt=""
      onError={() => setFailed(true)}
      sx={{ maxHeight: 40, maxWidth: 96, borderRadius: 0.5, objectFit: 'contain', flexShrink: 0 }}
    />
  )
}

// --- Left pane: projects → folders tree -------------------------------------

interface TreeCommon {
  hubId: string
  expanded: Set<string>
  onToggle: (id: string) => void
  currentId: string | null
  onGo: (project: Item, stack: Item[]) => void
}

function TreePane({
  filter,
  onFilter,
  ...c
}: TreeCommon & { filter: string; onFilter: (q: string) => void }) {
  const projectsQ = useProjects(c.hubId)
  const projects = useMemo(() => {
    const all = projectsQ.data ?? []
    const q = filter.trim().toLowerCase()
    return q ? all.filter((p) => p.name.toLowerCase().includes(q)) : all
  }, [projectsQ.data, filter])

  return (
    <Box
      sx={{
        width: 280,
        flexShrink: 0,
        borderRight: 1,
        borderColor: 'divider',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <Box sx={{ p: 1, borderBottom: 1, borderColor: 'divider' }}>
        <TextField
          value={filter}
          onChange={(e) => onFilter(e.target.value)}
          placeholder="Filter projects"
          size="small"
          fullWidth
          InputProps={{
            startAdornment: (
              <InputAdornment position="start">
                <FontAwesomeIcon icon={faMagnifyingGlass} style={{ fontSize: 12 }} />
              </InputAdornment>
            ),
          }}
        />
      </Box>
      <Box sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        {projectsQ.isLoading ? (
          <Box sx={{ p: 2, textAlign: 'center' }}>
            <CircularProgress size={18} />
          </Box>
        ) : projectsQ.error ? (
          <Alert severity="error" variant="outlined" sx={{ m: 1 }}>
            Couldn't load projects.
          </Alert>
        ) : (
          <List dense disablePadding>
            {projects.map((p) => (
              <ProjectNode key={p.id} project={p} c={c} />
            ))}
          </List>
        )}
      </Box>
    </Box>
  )
}

function ProjectNode({ project, c }: { project: Item; c: TreeCommon }) {
  const isOpen = c.expanded.has(project.id)
  const contentsQ = useBrowseContents(isOpen ? c.hubId : null, project.altId, '')
  const folders = (contentsQ.data ?? []).filter((i) => i.isContainer)
  return (
    <>
      <TreeRow
        depth={0}
        item={project}
        selected={c.currentId === project.id}
        open={isOpen}
        loading={isOpen && contentsQ.isLoading}
        onToggle={() => c.onToggle(project.id)}
        onClick={() => {
          c.onGo(project, [])
          if (!isOpen) c.onToggle(project.id)
        }}
      />
      {isOpen &&
        folders.map((f) => (
          <FolderNode key={f.id} project={project} folder={f} stack={[f]} depth={1} c={c} />
        ))}
    </>
  )
}

function FolderNode({
  project,
  folder,
  stack,
  depth,
  c,
}: {
  project: Item
  folder: Item
  stack: Item[]
  depth: number
  c: TreeCommon
}) {
  const isOpen = c.expanded.has(folder.id)
  const childrenQ = useBrowseContents(isOpen ? c.hubId : null, project.altId, folder.id)
  const folders = (childrenQ.data ?? []).filter((i) => i.isContainer)
  return (
    <>
      <TreeRow
        depth={depth}
        item={folder}
        selected={c.currentId === folder.id}
        open={isOpen}
        loading={isOpen && childrenQ.isLoading}
        onToggle={() => c.onToggle(folder.id)}
        onClick={() => {
          c.onGo(project, stack)
          if (!isOpen) c.onToggle(folder.id)
        }}
      />
      {isOpen &&
        folders.map((f) => (
          <FolderNode
            key={f.id}
            project={project}
            folder={f}
            stack={[...stack, f]}
            depth={depth + 1}
            c={c}
          />
        ))}
    </>
  )
}

function TreeRow({
  depth,
  item,
  selected,
  open,
  loading,
  onToggle,
  onClick,
}: {
  depth: number
  item: Item
  selected: boolean
  open: boolean
  loading: boolean
  onToggle: () => void
  onClick: () => void
}) {
  return (
    <ListItemButton
      dense
      selected={selected}
      onClick={onClick}
      sx={{ pl: 1 + depth * 1.75, pr: 1, py: 0.25, minHeight: 30, gap: 0.5 }}
    >
      <Box
        onClick={(e) => {
          e.stopPropagation()
          onToggle()
        }}
        sx={{
          width: 18,
          flexShrink: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: 'text.disabled',
        }}
      >
        {loading ? (
          <CircularProgress size={10} />
        ) : (
          <FontAwesomeIcon icon={open ? faChevronDown : faChevronRight} style={{ fontSize: 9 }} />
        )}
      </Box>
      <Box sx={{ width: 20, flexShrink: 0, textAlign: 'center', color: selected ? 'primary.main' : 'text.secondary' }}>
        <ItemIcon item={item} style={{ fontSize: 12 }} />
      </Box>
      <Typography variant="body2" noWrap title={item.name} sx={{ fontWeight: selected ? 600 : 400 }}>
        {item.name}
      </Typography>
    </ListItemButton>
  )
}

// --- Right pane: current location's contents ---------------------------------

function ContentsPane({
  hubId,
  project,
  folderStack,
  mode,
  selectable,
  doc,
  onGo,
  onSelectDoc,
  onPickDoc,
}: {
  hubId: string
  project: Item | null
  folderStack: Item[]
  mode: 'document' | 'folder'
  selectable?: (item: Item) => boolean
  doc: Item | null
  onGo: (project: Item, stack: Item[]) => void
  onSelectDoc: (item: Item) => void
  onPickDoc: (item: Item) => void
}) {
  const top = folderStack.length ? folderStack[folderStack.length - 1] : null
  const contentsQ = useBrowseContents(project ? hubId : null, project?.altId, top?.id ?? '')
  const loading = contentsQ.isLoading
  const error = contentsQ.error

  const rows = useMemo(
    () =>
      [...(contentsQ.data ?? [])].sort((a, b) =>
        a.isContainer !== b.isContainer ? (a.isContainer ? -1 : 1) : a.name.localeCompare(b.name),
      ),
    [contentsQ.data],
  )

  // When a folder has documents but the caller's filter rejects them all, say
  // so — greyed-out rows alone read as "nothing here to pick".
  const files = rows.filter((r) => !r.isContainer)
  const nonePickable =
    mode === 'document' && !!selectable && files.length > 0 && !files.some(selectable)

  if (!project) {
    return (
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', p: 3 }}>
        <Typography variant="body2" color="text.secondary">
          Select a project to browse.
        </Typography>
      </Box>
    )
  }
  if (!project.altId) {
    return (
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', p: 3 }}>
        <Typography variant="body2" color="text.secondary">
          This project can't be browsed (no Data Management id).
        </Typography>
      </Box>
    )
  }

  return (
    <Box sx={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <Breadcrumbs
        separator="›"
        sx={{ px: 1.5, py: 1, borderBottom: 1, borderColor: 'divider', fontSize: 13, flexShrink: 0 }}
      >
        {folderStack.length === 0 ? (
          <Typography variant="body2" sx={{ fontWeight: 600 }}>
            {project.name}
          </Typography>
        ) : (
          <Link component="button" variant="body2" underline="hover" onClick={() => onGo(project, [])}>
            {project.name}
          </Link>
        )}
        {folderStack.map((f, i) =>
          i === folderStack.length - 1 ? (
            <Typography key={f.id} variant="body2" sx={{ fontWeight: 600 }}>
              {f.name}
            </Typography>
          ) : (
            <Link
              key={f.id}
              component="button"
              variant="body2"
              underline="hover"
              onClick={() => onGo(project, folderStack.slice(0, i + 1))}
            >
              {f.name}
            </Link>
          ),
        )}
      </Breadcrumbs>

      <Box sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        {loading ? (
          <Box sx={{ p: 2, textAlign: 'center' }}>
            <CircularProgress size={18} />
          </Box>
        ) : error ? (
          <Alert severity="error" variant="outlined" sx={{ m: 1 }}>
            Couldn't load this folder.
          </Alert>
        ) : rows.length === 0 ? (
          <Typography variant="body2" color="text.secondary" sx={{ p: 2, textAlign: 'center' }}>
            This folder is empty.
          </Typography>
        ) : (
          <>
            <List dense disablePadding>
              {rows.map((item) => {
                if (item.isContainer) {
                  return (
                    <ContentsRow
                      key={item.id}
                      item={item}
                      selected={false}
                      disabled={false}
                      onClick={() => onGo(project, [...folderStack, item])}
                    />
                  )
                }
                const pickable = mode === 'document' && (selectable ? selectable(item) : true)
                return (
                  <ContentsRow
                    key={item.id}
                    item={item}
                    selected={doc?.id === item.id}
                    disabled={!pickable}
                    onClick={pickable ? () => onSelectDoc(item) : undefined}
                    onDoubleClick={pickable ? () => onPickDoc(item) : undefined}
                  />
                )
              })}
            </List>
            {nonePickable && (
              <Typography variant="caption" color="text.secondary" sx={{ px: 2, py: 1, display: 'block' }}>
                None of these documents can be picked here.
              </Typography>
            )}
          </>
        )}
      </Box>
    </Box>
  )
}

function ContentsRow({
  item,
  selected,
  disabled,
  onClick,
  onDoubleClick,
}: {
  item: Item
  selected: boolean
  disabled: boolean
  onClick?: () => void
  onDoubleClick?: () => void
}) {
  // Fall back to the file extension when the row has no design-type tag, so
  // plain uploads (images, PDFs, …) still read at a glance.
  const tag = typeTag(item) || (item.isContainer ? '' : extOf(item.name))
  return (
    <ListItemButton
      dense
      selected={selected}
      disabled={disabled && !item.isContainer}
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      sx={{ py: 0.4, minHeight: 32, gap: 1 }}
    >
      <Box sx={{ width: 20, flexShrink: 0, textAlign: 'center', color: selected ? 'primary.main' : 'text.secondary' }}>
        <FontAwesomeIcon icon={docIcon(item)} style={{ fontSize: 13 }} />
      </Box>
      <Box sx={{ minWidth: 0, display: 'flex', alignItems: 'baseline', gap: 0.75 }}>
        <Typography variant="body2" noWrap title={item.name} sx={{ fontWeight: selected ? 600 : 400 }}>
          {item.name}
        </Typography>
        {tag && (
          <Typography variant="caption" sx={{ color: 'text.disabled', flexShrink: 0, fontSize: 10 }}>
            · {tag}
          </Typography>
        )}
      </Box>
    </ListItemButton>
  )
}
