import {
  faDiagramProject,
  faFileCirclePlus,
  faListCheck,
  faPaperPlane,
  faSquarePlus,
} from '@fortawesome/free-solid-svg-icons'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { Box, IconButton, TextField, Tooltip, Typography } from '@mui/material'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { encodeDocRef, docRefFromItem } from '../components/doccard/docref'
import { encodeTaskRef, taskRefFromTask } from '../components/taskcard/taskref'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import { ProductionRefDialog } from '../production/ProductionRefDialog'
import { useNav } from '../state/nav'
import { AttachTaskDialog } from '../tasks/AttachTaskDialog'
import { QuickTaskDialog } from '../tasks/QuickTaskDialog'
import type { Task } from '../tasks/types'

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

  // appendToken drops a card token into the draft, spacing it off whatever
  // is already there so the unfurl regex can find it.
  const appendToken = (token: string) =>
    setText((t) => (t && !/\s$/.test(t) ? `${t} ` : t) + token + ' ')
  const appendTaskToken = (task: Task) => appendToken(encodeTaskRef(taskRefFromTask(task)))

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
    <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 0.5, p: 1, pt: 0.5 }}>
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
      <TextField
        fullWidth
        size="small"
        multiline
        maxRows={6}
        placeholder={placeholder}
        value={text}
        disabled={disabled}
        onChange={(e) => {
          setText(e.target.value)
          if (e.target.value.trim()) onTyping?.()
        }}
        onKeyDown={(e) => {
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
    </Box>
  )
}
