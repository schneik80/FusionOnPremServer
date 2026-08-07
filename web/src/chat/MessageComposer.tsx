import {
  faCamera,
  faChalkboard,
  faDiagramProject,
  faFileCirclePlus,
  faImage,
  faListCheck,
  faPaperPlane,
  faSquarePlus,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  Box,
  IconButton,
  MenuItem,
  MenuList,
  Paper,
  Popper,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { encodeDocRef, docRefFromItem } from '../components/doccard/docref'
import { encodeImgRef } from '../components/imgcard/imgref'
import { captureFusionScreenshot, hasFusionBridge } from '../components/imgcard/fusionCapture'
import { MarkupDialog } from '../components/imgcard/MarkupDialog'
import { encodeTaskRef, taskRefFromTask } from '../components/taskcard/taskref'
import { encodeWhiteboardRef, whiteboardRefFromBoard } from '../components/whiteboardcard/wbref'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
import { AttachWhiteboardDialog } from '../whiteboards/AttachWhiteboardDialog'
import { useChatMembers } from '../api/queries'
import { encodeMention } from '../notifications/mentions'
import { useNav } from '../state/nav'
import { AttachTaskDialog } from '../tasks/AttachTaskDialog'
import { QuickTaskDialog } from '../tasks/QuickTaskDialog'
import type { Task } from '../tasks/types'
import type { ChatMember } from './types'

// activeMention finds an in-progress "@query" immediately before the caret: an
// '@' at line start or after whitespace, followed by non-whitespace up to the
// caret. Returns the query and the '@' index so a pick can splice it out.
function activeMention(value: string, caret: number): { query: string; start: number } | null {
  for (let i = caret - 1; i >= 0; i--) {
    const ch = value[i]
    if (ch === '@') {
      const before = i === 0 ? ' ' : value[i - 1]
      if (!/\s/.test(before)) return null
      const query = value.slice(i + 1, caret)
      if (/\s/.test(query)) return null
      return { query, start: i }
    }
    if (/\s/.test(ch)) return null
  }
  return null
}

// MessageComposer is the send box for a channel or thread. Enter sends,
// Shift+Enter inserts a newline. Read-only roles see a disabled box with an
// explanation instead of bouncing off the server's 403. The attach button
// browses the hub and drops a doc-ref token into the draft, which the message
// list unfurls into a DocumentCard.
export function MessageComposer({
  placeholder,
  disabled,
  disabledReason,
  sending,
  onSend,
  onTyping,
}: {
  placeholder: string
  disabled: boolean
  disabledReason?: string
  sending: boolean
  onSend: (body: string) => Promise<unknown>
  // Called on keystrokes with content; the provider throttles the actual
  // typing pings (see useTypingPing).
  onTyping?: () => void
}) {
  const { t } = useTranslation('chat')
  const nav = useNav()
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [taskPickerOpen, setTaskPickerOpen] = useState(false)
  const [taskCreateOpen, setTaskCreateOpen] = useState(false)
  const [prodPickerOpen, setProdPickerOpen] = useState(false)
  const [boardPickerOpen, setBoardPickerOpen] = useState(false)

  // @mention autocomplete: a live "@query" before the caret opens a member
  // picker; choosing a member splices an fls:user token into the draft (which
  // the message list unfurls to a highlighted @Name and the server parses to
  // address the mention notification). Only where there's a project roster.
  const projectId = nav.project?.id ?? null
  const [mention, setMention] = useState<{ query: string; start: number } | null>(null)
  const [mentionIndex, setMentionIndex] = useState(0)
  const caretRef = useRef(0)
  const inputRef = useRef<HTMLTextAreaElement | null>(null)
  const anchorRef = useRef<HTMLDivElement | null>(null)
  const members = useChatMembers(projectId, mention !== null)
  const matches = useMemo(() => {
    if (!mention) return []
    const q = mention.query.toLowerCase()
    return (members.data ?? [])
      .filter(
        (m) =>
          !q ||
          m.name?.toLowerCase().includes(q) ||
          (m.email?.toLowerCase().includes(q) ?? false),
      )
      .slice(0, 8)
  }, [members.data, mention])
  useEffect(() => setMentionIndex(0), [mention?.query])

  const syncMention = (value: string, caret: number) => {
    caretRef.current = caret
    setMention(projectId ? activeMention(value, caret) : null)
  }

  const pickMention = (m: ChatMember) => {
    if (!mention) return
    const token = encodeMention({ userId: m.userId, name: m.name || m.email || '' })
    const before = text.slice(0, mention.start)
    const after = text.slice(caretRef.current)
    const tail = after.startsWith(' ') ? after : ' ' + after
    setText(before + token + tail)
    setMention(null)
    requestAnimationFrame(() => {
      const el = inputRef.current
      if (el) {
        const pos = (before + token + ' ').length
        el.focus()
        el.setSelectionRange(pos, pos)
      }
    })
  }

  // appendToken drops a card token into the draft, spacing it off whatever
  // is already there so the unfurl regex can find it.
  const appendToken = (token: string) =>
    setText((t) => (t && !/\s$/.test(t) ? `${t} ` : t) + token + ' ')
  const appendTaskToken = (task: Task) => appendToken(encodeTaskRef(taskRefFromTask(task)))

  // Image attachments upload to the project's Chat/images/ folder first (an
  // ordinary Fusion Team item), then ride the draft as an fls:img token. Needs
  // the project's altId (DM id) — absent for a beat on cold permalinks until
  // the nav backstop fills it, so the buttons hide rather than half-work.
  const [attaching, setAttaching] = useState(false)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const dmProjectId = nav.project?.altId ?? null
  const canAttachImage = !!(nav.hubId && nav.project && dmProjectId)
  const attachImage = async (file: File) => {
    if (!canAttachImage || attaching) return
    setError(null)
    setAttaching(true)
    try {
      const res = await api.chatUploadImage(
        { projectId: nav.project!.id, hubId: nav.hubId!, dmProjectId: dmProjectId! },
        file,
      )
      appendToken(encodeImgRef({ dmProjectId: dmProjectId!, itemId: res.itemId, name: res.name }))
    } catch (e) {
      setError(e instanceof Error ? e.message : t('composer.attachImageFailed'))
    } finally {
      setAttaching(false)
    }
  }
  // A capture opens the markup dialog first (redlines over the screenshot);
  // the upload happens on the dialog's Attach with the blended PNG.
  const [markupFile, setMarkupFile] = useState<File | null>(null)
  const captureShot = async () => {
    setError(null)
    const file = await captureFusionScreenshot()
    if (!file) {
      setError(t('composer.captureFailed'))
      return
    }
    setMarkupFile(file)
  }

  const send = async () => {
    const body = text.trim()
    if (!body || sending) return
    setError(null)
    try {
      await onSend(body)
      setText('')
    } catch (e) {
      setError(e instanceof Error ? e.message : t('composer.sendFailed'))
    }
  }

  const box = (
    <Box ref={anchorRef} sx={{ display: 'flex', alignItems: 'flex-end', gap: 0.5, p: 1, pt: 0.5 }}>
      {nav.hubId && (
        <Tooltip title={t('composer.attachDoc')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled}
              onClick={() => setPickerOpen(true)}
              aria-label={t('composer.attachDoc')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faFileCirclePlus} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {nav.project && (
        <Tooltip title={t('composer.attachTask')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled}
              onClick={() => setTaskPickerOpen(true)}
              aria-label={t('composer.attachTask')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faListCheck} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {nav.project && (
        <Tooltip title={t('composer.createTask')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled}
              onClick={() => setTaskCreateOpen(true)}
              aria-label={t('composer.createTask')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faSquarePlus} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {nav.project && (
        <Tooltip title={t('composer.linkProduction')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled}
              onClick={() => setProdPickerOpen(true)}
              aria-label={t('composer.linkProduction')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faDiagramProject} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {nav.project && (
        <Tooltip title={t('composer.attachWhiteboard')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled}
              onClick={() => setBoardPickerOpen(true)}
              aria-label={t('composer.attachWhiteboard')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faChalkboard} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {canAttachImage && (
        <Tooltip title={t('composer.attachImage')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled || attaching}
              onClick={() => fileInputRef.current?.click()}
              aria-label={t('composer.attachImage')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faImage} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      {canAttachImage && hasFusionBridge() && (
        <Tooltip title={t('composer.captureShot')}>
          <span>
            <IconButton
              size="small"
              disabled={disabled || attaching}
              onClick={() => void captureShot()}
              aria-label={t('composer.captureShot')}
              sx={{ color: 'text.secondary' }}
            >
              <FontAwesomeIcon icon={faCamera} />
            </IconButton>
          </span>
        </Tooltip>
      )}
      <TextField
        fullWidth
        size="small"
        multiline
        maxRows={6}
        placeholder={placeholder}
        value={text}
        disabled={disabled}
        inputRef={inputRef}
        onChange={(e) => {
          setText(e.target.value)
          syncMention(e.target.value, e.target.selectionStart ?? e.target.value.length)
          if (e.target.value.trim()) onTyping?.()
        }}
        onSelect={() => {
          const el = inputRef.current
          if (el) syncMention(el.value, el.selectionStart ?? el.value.length)
        }}
        onKeyDown={(e) => {
          // While the mention picker is open, the arrow/enter/tab keys drive
          // it instead of the composer.
          if (matches.length > 0) {
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setMentionIndex((i) => (i + 1) % matches.length)
              return
            }
            if (e.key === 'ArrowUp') {
              e.preventDefault()
              setMentionIndex((i) => (i - 1 + matches.length) % matches.length)
              return
            }
            if (e.key === 'Enter' || e.key === 'Tab') {
              e.preventDefault()
              pickMention(matches[mentionIndex])
              return
            }
            if (e.key === 'Escape') {
              e.preventDefault()
              setMention(null)
              return
            }
          }
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault()
            void send()
          }
        }}
      />
      <IconButton
        size="small"
        color="primary"
        disabled={disabled || sending || !text.trim()}
        onClick={() => void send()}
        aria-label={t('composer.send')}
      >
        <FontAwesomeIcon icon={faPaperPlane} />
      </IconButton>
    </Box>
  )

  return (
    <Box sx={{ borderTop: 1, borderColor: 'divider' }}>
      {markupFile && (
        <MarkupDialog
          file={markupFile}
          onClose={() => setMarkupFile(null)}
          onAttach={(f) => {
            setMarkupFile(null)
            void attachImage(f)
          }}
        />
      )}
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0]
          e.target.value = ''
          if (file) void attachImage(file)
        }}
      />
      {error && (
        <Typography variant="caption" color="error" sx={{ px: 1.5, pt: 0.5, display: 'block' }}>
          {error}
        </Typography>
      )}
      {disabled && disabledReason ? (
        <Tooltip title={disabledReason}>
          <span>{box}</span>
        </Tooltip>
      ) : (
        box
      )}
      <Popper
        open={matches.length > 0}
        anchorEl={anchorRef.current}
        placement="top-start"
        style={{ zIndex: 1300, width: anchorRef.current?.clientWidth }}
      >
        <Paper elevation={4} sx={{ maxHeight: 220, overflowY: 'auto', mb: 0.5 }}>
          <MenuList dense disablePadding>
            {matches.map((m, i) => (
              <MenuItem
                key={m.userId}
                selected={i === mentionIndex}
                // onMouseDown (not onClick) so the pick fires before the
                // textarea's blur can close the popper and drop the caret.
                onMouseDown={(e) => {
                  e.preventDefault()
                  pickMention(m)
                }}
              >
                <Typography variant="body2" noWrap>
                  {m.name || m.email || m.userId}
                </Typography>
              </MenuItem>
            ))}
          </MenuList>
        </Paper>
      </Popper>
      {nav.hubId && (
        <HubBrowserDialog
          open={pickerOpen}
          hubId={nav.hubId}
          title={t('composer.attachDoc')}
          initialProject={nav.project}
          pickLabel={t('composer.attach')}
          onClose={() => setPickerOpen(false)}
          onPick={(pick) => {
            setPickerOpen(false)
            if (!pick.item) return
            appendToken(encodeDocRef(docRefFromItem(pick.hubId, pick.item)))
          }}
        />
      )}
      {nav.project && taskPickerOpen && (
        <AttachTaskDialog
          open={taskPickerOpen}
          projectId={nav.project.id}
          onClose={() => setTaskPickerOpen(false)}
          onPick={(task) => {
            setTaskPickerOpen(false)
            appendTaskToken(task)
          }}
        />
      )}
      {nav.project && taskCreateOpen && (
        <QuickTaskDialog
          open={taskCreateOpen}
          onClose={() => setTaskCreateOpen(false)}
          projectId={nav.project.id}
          hubId={nav.hubId ?? ''}
          projectName={nav.project.name}
          onCreated={appendTaskToken}
        />
      )}
      {nav.project && prodPickerOpen && (
        <ProductionRefDialog
          open={prodPickerOpen}
          projectId={nav.project.id}
          hubId={nav.hubId ?? ''}
          projectName={nav.project.name}
          onClose={() => setProdPickerOpen(false)}
          onPick={appendToken}
        />
      )}
      {nav.project && boardPickerOpen && (
        <AttachWhiteboardDialog
          open={boardPickerOpen}
          projectId={nav.project.id}
          onClose={() => setBoardPickerOpen(false)}
          onPick={(board) => {
            setBoardPickerOpen(false)
            appendToken(encodeWhiteboardRef(whiteboardRefFromBoard(board)))
          }}
        />
      )}
    </Box>
  )
}
