import { faTrash } from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  Button,
  IconButton,
  List,
  ListItemButton,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { alpha } from '@mui/material/styles'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthMe, useWhiteboardMutations, useWhiteboards } from '../api/queries'
import { APP_RAIL_WIDTH } from '../components/Column'
import { PinStar } from '../components/PinStar'
import { RailHeader } from '../components/RailHeader'
import { encodeWhiteboardRef, whiteboardRefFromBoard } from '../components/whiteboardcard/wbref'
import { useNav } from '../state/nav'
import { useLocalPin } from '../state/pins'
import { LazyBoardCanvas } from './LazyBoardCanvas'
import { boardDisplayId } from './types'
import type { Whiteboard } from './types'

// WhiteboardsApp is the project-tab whiteboard manager (the WikiApp/ChatApp
// contract: `active` gates fetching to the visible tab). A rail of boards on the
// left, the selected board's canvas on the right — the same master/detail shape
// as Tasks and Production, so the tab strip stays predictable.
export function WhiteboardsApp({ active = true }: { active?: boolean }) {
  const { t } = useTranslation('whiteboards')
  const nav = useNav()
  const projectId = nav.project?.id ?? null
  const q = useWhiteboards(projectId, active)
  const me = useAuthMe().data?.user

  const boards = q.data?.whiteboards ?? []
  const caps = q.data?.capabilities
  const canWrite = caps?.write ?? false

  // The selection lives in nav, not here: an fls:whiteboard card in any
  // project's chat opens a board by putting its id there, and it makes a board
  // permalinkable (?ptab=whiteboards&board=w3).
  //
  // It is still LATCHED rather than derived per render: the list refetches
  // every 15s newest-first, so a `?? boards[0]` fallback would swap the open
  // board whenever a teammate created one. An id that isn't in the list (a
  // deleted board, a stale permalink) falls back to the newest.
  const selected = boards.find((b) => b.id === nav.boardId) ?? null
  useEffect(() => {
    if (!selected && boards.length > 0) nav.selectBoard(boards[0].id)
  }, [selected, boards, nav])

  if (!projectId) return null

  return (
    <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex' }}>
      <BoardRail
        projectId={projectId}
        hubId={nav.hubId ?? ''}
        projectName={nav.project?.name ?? ''}
        boards={boards}
        canWrite={canWrite}
        canModerate={caps?.moderate ?? false}
        myId={me?.id ?? ''}
        loading={q.isLoading}
        error={q.error as Error | null}
        selectedId={selected?.id ?? null}
        onSelect={nav.selectBoard}
        onDeleted={() => nav.selectBoard(null)}
      />
      <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: 'flex', position: 'relative' }}>
        {selected ? (
          <LazyBoardCanvas projectId={projectId} boardId={selected.id} canWrite={canWrite} />
        ) : (
          <Box
            sx={{
              flex: 1,
              display: 'grid',
              placeItems: 'center',
              color: 'text.secondary',
              fontSize: 13,
              px: 3,
              textAlign: 'center',
            }}
          >
            {q.isLoading
              ? t('list.loading')
              : canWrite
                ? t('emptyState.canWrite')
                : t('emptyState.readOnly')}
          </Box>
        )}
      </Box>
    </Box>
  )
}

function BoardRail({
  projectId,
  hubId,
  projectName,
  boards,
  canWrite,
  canModerate,
  myId,
  loading,
  error,
  selectedId,
  onSelect,
  onDeleted,
}: {
  projectId: string
  hubId: string
  projectName: string
  boards: Whiteboard[]
  canWrite: boolean
  canModerate: boolean
  myId: string
  loading: boolean
  error: Error | null
  selectedId: string | null
  onSelect: (id: string) => void
  onDeleted: () => void
}) {
  const { t } = useTranslation('whiteboards')
  const { create, rename, remove } = useWhiteboardMutations(projectId)
  const pin = useLocalPin()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState('')

  const submit = () => {
    const trimmed = name.trim()
    if (!trimmed) return
    create.mutate(
      { hubId, projectName, name: trimmed },
      {
        onSuccess: (b) => {
          onSelect(b.id)
          setName('')
          setAdding(false)
        },
      },
    )
  }

  const commitRename = (boardId: string, original: string) => {
    const next = renameDraft.trim()
    if (next && next !== original) rename.mutate({ boardId, patch: { name: next } })
    setRenamingId(null)
  }

  return (
    <Box
      sx={{
        width: APP_RAIL_WIDTH,
        flexShrink: 0,
        borderRight: 1,
        borderColor: 'divider',
        display: 'flex',
        flexDirection: 'column',
        minHeight: 0,
      }}
    >
      <RailHeader
        title={t('list.title')}
        onNew={() => setAdding((v) => !v)}
        newDisabled={!canWrite}
        newDisabledReason={loading ? '' : t('list.readOnlyReason')}
      />

      {adding && (
        <Box sx={{ p: 1, borderBottom: 1, borderColor: 'divider' }}>
          <TextField
            autoFocus
            fullWidth
            size="small"
            placeholder={t('list.namePlaceholder')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
              if (e.key === 'Escape') {
                setAdding(false)
                setName('')
              }
            }}
          />
          <Stack direction="row" spacing={1} sx={{ mt: 1 }} justifyContent="flex-end">
            <Button size="small" onClick={() => setAdding(false)} sx={{ textTransform: 'none' }}>
              {t('common:cancel')}
            </Button>
            <Button
              size="small"
              variant="contained"
              onClick={submit}
              disabled={!name.trim() || create.isPending}
              sx={{ textTransform: 'none' }}
            >
              {t('common:create')}
            </Button>
          </Stack>
        </Box>
      )}

      <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
        {error ? (
          <Typography variant="caption" color="error" sx={{ p: 2, display: 'block' }}>
            {t('list.loadFailed')}
          </Typography>
        ) : boards.length === 0 && !loading ? (
          <Typography variant="caption" color="text.secondary" sx={{ p: 2, display: 'block' }}>
            {t('list.empty')}
          </Typography>
        ) : (
          <List dense disablePadding>
            {boards.map((b) => {
              const canDelete = canModerate || b.createdBy.id === myId
              // The board's own fls: token is its pin identity — the same one
              // an fls:whiteboard card in a chat message carries.
              const token = encodeWhiteboardRef(whiteboardRefFromBoard(b))
              const pinned = pin.isPinned(token)
              return (
                <ListItemButton
                  key={b.id}
                  selected={b.id === selectedId}
                  onClick={() => onSelect(b.id)}
                  onDoubleClick={() => {
                    if (!canWrite) return
                    setRenamingId(b.id)
                    setRenameDraft(b.name)
                  }}
                  sx={{
                    gap: 0.5,
                    py: 0.75,
                    transition: 'background-color .1s',
                    '&.Mui-selected': { bgcolor: (t) => alpha(t.palette.primary.main, 0.12) },
                    '&:hover .wb-del': { opacity: 1 },
                    // Same posture as an ItemRow's star: quiet until hovered,
                    // permanent once the board is pinned.
                    '& .pin-star': { opacity: pinned ? 1 : 0, transition: 'opacity .1s' },
                    '&:hover .pin-star, &:focus-within .pin-star': { opacity: 1 },
                  }}
                >
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    {renamingId === b.id ? (
                      <TextField
                        autoFocus
                        fullWidth
                        size="small"
                        variant="standard"
                        value={renameDraft}
                        onClick={(e) => e.stopPropagation()}
                        onChange={(e) => setRenameDraft(e.target.value)}
                        onBlur={() => commitRename(b.id, b.name)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                          if (e.key === 'Escape') setRenamingId(null)
                        }}
                        sx={{ '& input': { fontSize: 13, fontWeight: 600 } }}
                      />
                    ) : (
                      <Typography variant="body2" fontWeight={600} noWrap>
                        {b.name}
                      </Typography>
                    )}
                    <Typography variant="caption" color="text.secondary" noWrap>
                      {boardDisplayId(b)} · {new Date(b.updatedAt).toLocaleDateString()}
                      {b.updatedBy.name ? ` · ${b.updatedBy.name}` : ''}
                    </Typography>
                  </Box>
                  <PinStar
                    pinned={pinned}
                    onToggle={() =>
                      pin.toggle({
                        ref: token,
                        kind: 'whiteboard',
                        name: b.name,
                        projectId: b.projectId,
                        projectName: b.projectName,
                      })
                    }
                    fontSize={11}
                  />
                  {canDelete && (
                    <Tooltip title={t('list.deleteTooltip')}>
                      <IconButton
                        size="small"
                        className="wb-del"
                        sx={{ opacity: 0, transition: 'opacity .1s', flexShrink: 0 }}
                        onClick={(e) => {
                          e.stopPropagation()
                          if (window.confirm(t('list.deleteConfirm', { name: b.name }))) {
                            remove.mutate(b.id, { onSuccess: onDeleted })
                          }
                        }}
                      >
                        <FontAwesomeIcon icon={faTrash} style={{ fontSize: 11 }} />
                      </IconButton>
                    </Tooltip>
                  )}
                </ListItemButton>
              )
            })}
          </List>
        )}
      </Box>

      {canWrite && boards.length > 0 && (
        <Typography variant="caption" color="text.disabled" sx={{ px: 1.5, py: 0.75, borderTop: 1, borderColor: 'divider' }}>
          {t('list.renameHint')}
        </Typography>
      )}
    </Box>
  )
}
