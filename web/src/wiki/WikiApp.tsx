import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Stack,
  TextField,
  Typography,
} from '@mui/material'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '../api/client'
import { useWikiPage, useWikiPages, useWikiPublish, useWikiRename } from '../api/queries'
import { useNav } from '../state/nav'
import { slugify, type WikiDraft } from './draftStore'
import { MarkdownView } from './MarkdownView'
import { useWikiDrafts } from './useDrafts'
import { WikiEditor } from './WikiEditor'
import { WikiSidebar, type WikiEntry, type WikiEntryStatus } from './WikiSidebar'
import { foldSearch } from '../fmt/graphemes'

// reconcileStatus derives a linked draft's shown status by comparing what it was
// based on against the page's live tip: the remote moving ahead is 'behind' when
// the local copy is clean, or 'conflict' when the local copy also has edits.
function reconcileStatus(d: WikiDraft, tipVersion?: string): WikiEntryStatus {
  const localEdited = d.status === 'modified'
  const remoteAdvanced = !!tipVersion && !!d.baseVersion && tipVersion !== d.baseVersion
  if (remoteAdvanced && localEdited) return 'conflict'
  if (remoteAdvanced) return 'behind'
  return d.status
}

// Autosave debounce: how long after the last keystroke the working copy is
// flushed to IndexedDB.
const AUTOSAVE_MS = 600

// WikiApp is the whole project wiki experience, mounted inside the project-level
// tab shell (and reused when the browser drills into the Wiki folder). Left: a
// flat list merging published pages with local drafts. Right: a reader, or the
// split-pane editor. Publishing (upload to APS) is Phase 2; everything here —
// authoring local drafts, reading published pages, pulling one down as a draft —
// works under the current read-only scope.
export function WikiApp({ active = true }: { active?: boolean }) {
  const { t } = useTranslation('wiki')
  const nav = useNav()
  const project = nav.project
  const hubId = nav.hubId
  const dmProjectId = project?.altId ?? null
  const projectId = project?.id ?? null

  // The pane stays mounted while the Dashboard tab is showing; gate the published
  // -pages fetch on the Wiki tab actually being open so it costs no APS calls for
  // users who never visit it (mirrors DetailsPanel's per-tab query gating).
  const pagesQ = useWikiPages(hubId, dmProjectId, active)
  const { drafts, loading: draftsLoading, save, remove, create, importPage } = useWikiDrafts(projectId)
  const publishMut = useWikiPublish(hubId, dmProjectId)
  const renameMut = useWikiRename(hubId, dmProjectId)

  // Writes (publish, image upload, rename) need the project's data-management id.
  const canWrite = !!hubId && !!dmProjectId

  // Keep a ref to the latest drafts so effects/handlers can read them without
  // taking `drafts` as a dependency (which would retrigger on every save).
  const draftsRef = useRef<WikiDraft[]>(drafts)
  draftsRef.current = drafts

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  // Editor working copy (source of truth while editing; flushed on a debounce).
  const [workingMd, setWorkingMd] = useState('')
  const [workingTitle, setWorkingTitle] = useState('')
  const [saved, setSaved] = useState(true)

  // Rename dialog: the entry being renamed and the working title text.
  const [renameTarget, setRenameTarget] = useState<WikiEntry | null>(null)
  const [renameValue, setRenameValue] = useState('')

  // This pane stays mounted across project switches (BrowserStage keeps slot B
  // panes alive), so clear any selection/edit that belongs to the prior project.
  useEffect(() => {
    setSelectedId(null)
    setEditingKey(null)
  }, [projectId])

  // Freshen the published-pages list when the tab is opened, so a page another
  // device published surfaces as behind/updated rather than staying "synced".
  useEffect(() => {
    if (active) void pagesQ.refetch()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  // Merge published pages and local drafts into one flat, searchable list. A
  // draft linked to a published page (baseItemId) supersedes the remote row.
  const entries = useMemo<WikiEntry[]>(() => {
    const pages = pagesQ.data ?? []
    const linked = new Map<string, WikiDraft>()
    const localOnly: WikiDraft[] = []
    for (const d of drafts) {
      if (d.baseItemId) linked.set(d.baseItemId, d)
      else localOnly.push(d)
    }
    const out: WikiEntry[] = []
    for (const p of pages) {
      const d = linked.get(p.itemId)
      if (d) {
        out.push({ id: d.key, kind: 'draft', title: d.title, status: reconcileStatus(d, p.tipVersion), draftKey: d.key, itemId: p.itemId, modifiedOn: p.modifiedOn })
      } else {
        out.push({ id: p.itemId, kind: 'page', title: p.title, status: 'remote', itemId: p.itemId, modifiedOn: p.modifiedOn })
      }
    }
    for (const d of localOnly) {
      out.push({ id: d.key, kind: 'draft', title: d.title, status: d.status, draftKey: d.key })
    }
    out.sort((a, b) => a.title.localeCompare(b.title))
    const q = foldSearch(query.trim())
    return q ? out.filter((e) => foldSearch(e.title).includes(q)) : out
  }, [pagesQ.data, drafts, query])

  const selectedEntry = entries.find((e) => e.id === selectedId) ?? null
  const editingDraft = editingKey ? drafts.find((d) => d.key === editingKey) ?? null : null

  function beginEdit(d: WikiDraft) {
    setEditingKey(d.key)
    setSelectedId(d.key)
    setWorkingMd(d.markdown)
    setWorkingTitle(d.title)
    setSaved(true)
  }

  // Autosave the working copy while editing. Reads the baseline from the ref so
  // it doesn't re-run when `save()` reloads the drafts list.
  useEffect(() => {
    if (!editingKey) return
    const base = draftsRef.current.find((d) => d.key === editingKey)
    if (!base) return
    if (base.markdown === workingMd && base.title === workingTitle) {
      setSaved(true)
      return
    }
    setSaved(false)
    const timer = setTimeout(() => {
      void save({
        ...base,
        title: workingTitle || t('untitledPage'),
        // Once linked to a published page, the slug (filename) is frozen — only
        // an explicit Rename changes it, so title edits don't silently refile.
        slug: base.baseItemId ? base.slug : slugify(workingTitle || 'untitled'),
        markdown: workingMd,
        status: base.baseItemId ? 'modified' : 'draft',
        updatedAt: Date.now(),
      }).then(() => setSaved(true))
    }, AUTOSAVE_MS)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workingMd, workingTitle, editingKey])

  async function closeEditor() {
    const base = editingKey ? draftsRef.current.find((d) => d.key === editingKey) : null
    if (base && (base.markdown !== workingMd || base.title !== workingTitle)) {
      await save({
        ...base,
        title: workingTitle || t('untitledPage'),
        slug: base.baseItemId ? base.slug : slugify(workingTitle || 'untitled'),
        markdown: workingMd,
        status: base.baseItemId ? 'modified' : 'draft',
        updatedAt: Date.now(),
      })
    }
    setEditingKey(null)
  }

  async function handleNew() {
    const d = await create(t('untitledPage'))
    beginEdit(d)
  }

  async function handleDelete(key: string) {
    await remove(key)
    if (selectedId === key) setSelectedId(null)
    if (editingKey === key) setEditingKey(null)
  }

  async function handleEditAsDraft(payload: { itemId: string; title: string; tipVersion?: string; markdown: string }) {
    const d = await importPage(payload)
    beginEdit(d)
  }

  // handlePublish uploads the working copy to the project's Wiki folder, then
  // relinks the draft to the returned page/version and marks it published. On a
  // 409 (the page moved upstream) it offers to overwrite.
  async function handlePublish(force = false): Promise<void> {
    if (!editingDraft || !canWrite) return
    const title = workingTitle || editingDraft.title || t('untitledPage')
    // A linked page keeps its published filename; a new page derives it from the
    // title. Renaming a published page is a separate, explicit action.
    const slug = editingDraft.baseItemId ? editingDraft.slug : slugify(title || 'untitled')
    try {
      const page = await publishMut.mutateAsync({
        itemId: editingDraft.baseItemId ?? '',
        slug,
        markdown: workingMd,
        baseVersion: editingDraft.baseVersion ?? '',
        force,
      })
      await save({
        ...editingDraft,
        title,
        slug,
        markdown: workingMd,
        baseItemId: page.itemId,
        baseVersion: page.tipVersion,
        status: 'published',
        updatedAt: Date.now(),
      })
      setSaved(true)
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        // eslint-disable-next-line no-alert
        if (window.confirm(t('publish.conflictConfirm'))) {
          await handlePublish(true)
        }
        return
      }
      // eslint-disable-next-line no-alert
      alert(e instanceof Error ? e.message : t('alerts.publishFailed'))
    }
  }

  function openRename(entry: WikiEntry) {
    setRenameTarget(entry)
    setRenameValue(entry.title)
  }

  // doRename renames the page: for a published page (or linked draft) it renames
  // the APS file + images folder; for any draft it also updates the local title
  // and (frozen) slug so they stay in sync.
  async function doRename() {
    const entry = renameTarget
    const newTitle = renameValue.trim()
    if (!entry || !newTitle) return
    const newSlug = slugify(newTitle)
    const draft = entry.draftKey ? drafts.find((d) => d.key === entry.draftKey) ?? null : null
    const publishedItemId = draft?.baseItemId ?? entry.itemId
    const oldSlug = draft?.slug ?? entry.title
    try {
      if (publishedItemId && canWrite) {
        await renameMut.mutateAsync({ itemId: publishedItemId, oldSlug, newSlug })
      }
      if (draft) {
        await save({ ...draft, title: newTitle, slug: newSlug, updatedAt: Date.now() })
      }
      if (editingKey && editingKey === draft?.key) setWorkingTitle(newTitle)
      setRenameTarget(null)
    } catch (e) {
      // eslint-disable-next-line no-alert
      alert(e instanceof Error ? e.message : t('alerts.renameFailed'))
    }
  }

  // uploadImage stores an image under Wiki/<slug>/images/ and resolves to the
  // markdown src + alt the editor embeds. The slug matches the page being edited.
  async function uploadImage(file: File): Promise<{ src: string; alt: string }> {
    if (!editingDraft || !hubId || !dmProjectId) throw new Error('cannot upload image')
    const slug = slugify(workingTitle || editingDraft.title || 'untitled')
    const res = await api.wikiUploadImage({ hubId, dmProjectId, slug }, file)
    return {
      src: api.wikiImageUrl(dmProjectId, res.itemId),
      alt: res.name.replace(/\.[^./\\]+$/, ''),
    }
  }

  // handleRefresh pulls the latest published markdown into the linked draft,
  // adopting the live tip as the new base. For a 'behind' page this is a safe
  // update (nothing local to lose); for a 'conflict' it's the "take theirs"
  // choice (local edits are discarded).
  async function handleRefresh(entry: WikiEntry) {
    if (!entry.draftKey || !dmProjectId || !entry.itemId) return
    const draft = drafts.find((d) => d.key === entry.draftKey)
    if (!draft) return
    try {
      const content = await api.wikiPage(dmProjectId, entry.itemId)
      const page = pagesQ.data?.find((p) => p.itemId === entry.itemId)
      await save({
        ...draft,
        markdown: content.markdown,
        baseVersion: page?.tipVersion ?? draft.baseVersion,
        status: 'published',
        updatedAt: Date.now(),
      })
      if (editingKey === draft.key) {
        setWorkingMd(content.markdown)
        setSaved(true)
      }
    } catch (e) {
      // eslint-disable-next-line no-alert
      alert(e instanceof Error ? e.message : t('alerts.refreshFailed'))
    }
  }

  return (
    <Box sx={{ display: 'flex', flex: 1, minWidth: 0, minHeight: 0 }}>
      <WikiSidebar
        entries={entries}
        selectedId={selectedId}
        onSelect={(e) => {
          setSelectedId(e.id)
          setEditingKey(null)
          // Background lookup: re-check the live tips so the just-selected page's
          // status reflects any version published elsewhere.
          void pagesQ.refetch()
        }}
        onNew={handleNew}
        loading={draftsLoading || pagesQ.isLoading}
        query={query}
        onQuery={setQuery}
      />

      <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        {pagesQ.error && (
          <Alert severity="warning" variant="outlined" square sx={{ borderLeft: 0, borderRight: 0, borderTop: 0 }}>
            {t('alerts.pagesLoadFailed')}
          </Alert>
        )}

        {editingDraft ? (
          <WikiEditor
            draft={editingDraft}
            markdownValue={workingMd}
            titleValue={workingTitle}
            onChangeMarkdown={setWorkingMd}
            onChangeTitle={setWorkingTitle}
            onDiscard={closeEditor}
            saved={saved}
            onPublish={canWrite ? () => void handlePublish() : undefined}
            publishing={publishMut.isPending}
            onUploadImage={canWrite ? uploadImage : undefined}
            hubId={hubId}
            hubProject={project}
          />
        ) : selectedEntry?.kind === 'draft' ? (
          <DraftReader
            draft={drafts.find((d) => d.key === selectedEntry.draftKey) ?? null}
            status={selectedEntry.status}
            onEdit={beginEdit}
            onDelete={handleDelete}
            onRename={() => selectedEntry && openRename(selectedEntry)}
            onRefresh={() => selectedEntry && handleRefresh(selectedEntry)}
          />
        ) : selectedEntry?.kind === 'page' ? (
          <PublishedReader
            dmProjectId={dmProjectId}
            page={pagesQ.data?.find((p) => p.itemId === selectedEntry.itemId) ?? null}
            onEditAsDraft={handleEditAsDraft}
            onRename={canWrite ? () => selectedEntry && openRename(selectedEntry) : undefined}
          />
        ) : (
          <EmptyState onNew={handleNew} />
        )}
      </Box>

      <RenameDialog
        open={!!renameTarget}
        value={renameValue}
        busy={renameMut.isPending}
        onChange={setRenameValue}
        onCancel={() => setRenameTarget(null)}
        onSave={doRename}
      />
    </Box>
  )
}

function RenameDialog({
  open,
  value,
  busy,
  onChange,
  onCancel,
  onSave,
}: {
  open: boolean
  value: string
  busy: boolean
  onChange: (v: string) => void
  onCancel: () => void
  onSave: () => void
}) {
  const { t } = useTranslation('wiki')
  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth>
      <DialogTitle>{t('renameDialog.title')}</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          fullWidth
          variant="standard"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') onSave()
          }}
          sx={{ mt: 1 }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel} color="inherit">
          {t('common:cancel')}
        </Button>
        <Button onClick={onSave} variant="contained" disabled={busy || !value.trim()}>
          {busy ? t('renameDialog.renaming') : t('renameDialog.rename')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function EmptyState({ onNew }: { onNew: () => void }) {
  const { t } = useTranslation('wiki')
  return (
    <Stack spacing={1.5} alignItems="center" justifyContent="center" sx={{ flex: 1, p: 4, textAlign: 'center' }}>
      <Typography variant="h6" color="text.secondary">
        {t('empty.title')}
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 360 }}>
        {t('empty.body')}
      </Typography>
      <Button variant="outlined" onClick={onNew}>
        {t('empty.newPage')}
      </Button>
    </Stack>
  )
}

// DraftReader shows a local draft's rendered markdown with edit/rename/delete,
// plus a banner + refresh action when the published page has moved ahead.
function DraftReader({
  draft,
  status,
  onEdit,
  onDelete,
  onRename,
  onRefresh,
}: {
  draft: WikiDraft | null
  status: WikiEntryStatus
  onEdit: (d: WikiDraft) => void
  onDelete: (key: string) => void
  onRename: () => void
  onRefresh: () => void
}) {
  const { t } = useTranslation('wiki')
  if (!draft) return <EmptyState onNew={() => {}} />
  const behind = status === 'behind'
  const conflict = status === 'conflict'
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        sx={{ px: 2, py: 1, borderBottom: 1, borderColor: 'divider' }}
      >
        <Typography variant="h6" noWrap sx={{ flex: 1, minWidth: 0, fontWeight: 600 }}>
          {draft.title}
        </Typography>
        <StatusChip status={status} />
        <Button size="small" variant="contained" onClick={() => onEdit(draft)}>
          {t('common:edit')}
        </Button>
        <Button size="small" color="inherit" onClick={onRename}>
          {t('reader.rename')}
        </Button>
        <Button size="small" color="error" onClick={() => onDelete(draft.key)}>
          {t('common:delete')}
        </Button>
      </Stack>
      {(behind || conflict) && (
        <Alert
          severity={conflict ? 'warning' : 'info'}
          square
          sx={{ borderRadius: 0 }}
          action={
            <Button color="inherit" size="small" onClick={onRefresh}>
              {conflict ? t('reader.takeLatest') : t('reader.update')}
            </Button>
          }
        >
          {conflict ? t('reader.conflictBody') : t('reader.behindBody')}
        </Alert>
      )}
      <MarkdownView>{draft.markdown}</MarkdownView>
    </Box>
  )
}

// PublishedReader fetches and renders a published page, offering "Edit as draft"
// to pull it into the local editor.
function PublishedReader({
  dmProjectId,
  page,
  onEditAsDraft,
  onRename,
}: {
  dmProjectId: string | null
  page: { itemId: string; title: string; tipVersion?: string } | null
  onEditAsDraft: (payload: { itemId: string; title: string; tipVersion?: string; markdown: string }) => void
  onRename?: () => void
}) {
  const { t } = useTranslation('wiki')
  const contentQ = useWikiPage(dmProjectId, page?.itemId ?? null, !!page)

  if (!page) return null
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
      <Stack
        direction="row"
        spacing={1}
        alignItems="center"
        sx={{ px: 2, py: 1, borderBottom: 1, borderColor: 'divider' }}
      >
        <Typography variant="h6" noWrap sx={{ flex: 1, minWidth: 0, fontWeight: 600 }}>
          {page.title}
        </Typography>
        <StatusChip status="remote" />
        <Button
          size="small"
          variant="contained"
          disabled={!contentQ.data}
          onClick={() =>
            contentQ.data &&
            onEditAsDraft({
              itemId: page.itemId,
              title: page.title,
              tipVersion: page.tipVersion,
              markdown: contentQ.data.markdown,
            })
          }
        >
          {t('reader.editAsDraft')}
        </Button>
        {onRename && (
          <Button size="small" color="inherit" onClick={onRename}>
            {t('reader.rename')}
          </Button>
        )}
      </Stack>
      {contentQ.isLoading ? (
        <Box sx={{ flex: 1, p: 2, textAlign: 'center' }}>
          <CircularProgress size={18} />
        </Box>
      ) : contentQ.error ? (
        <Box sx={{ flex: 1, p: 2 }}>
          <Alert severity="error" variant="outlined">
            {t('reader.downloadFailed')}
          </Alert>
        </Box>
      ) : (
        <MarkdownView>{contentQ.data?.markdown ?? ''}</MarkdownView>
      )}
    </Box>
  )
}

function StatusChip({ status }: { status: WikiEntryStatus }) {
  const { t } = useTranslation('wiki')
  const meta =
    status === 'draft'
      ? { label: t('status.localDraft'), color: 'default' as const }
      : status === 'modified'
        ? { label: t('status.unpublishedChanges'), color: 'warning' as const }
        : status === 'behind'
          ? { label: t('status.updateAvailable'), color: 'info' as const }
          : status === 'conflict'
            ? { label: t('status.conflict'), color: 'error' as const }
            : status === 'published'
              ? { label: t('status.synced'), color: 'success' as const }
              : { label: t('status.published'), color: 'info' as const }
  return <Chip size="small" label={meta.label} color={meta.color} variant="outlined" sx={{ height: 20 }} />
}
