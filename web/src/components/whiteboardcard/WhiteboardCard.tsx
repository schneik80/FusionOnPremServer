import { faChalkboard } from '@fortawesome/free-solid-svg-icons'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useWhiteboard } from '../../api/queries'
import { fmtBytes, fmtDate } from '../../fmt'
import { useNav } from '../../state/nav'
import { boardDisplayId } from '../../whiteboards/types'
import { EntityCard, type CardMeta } from '../entitycard/EntityCard'
import { WhiteboardViewDialog } from './WhiteboardViewDialog'
import type { WhiteboardRef } from './wbref'

// WhiteboardCard is the unfurled form of a WhiteboardRef (see wbref.ts) — the
// same shared EntityCard as the document, task and production cards, hydrating
// from the project's board list and opening the live board in a dialog. A card
// can sit in any project's chat, wiki or task body, so it must not depend on
// the browser's nav state beyond the hub check below.
//
// A board that has been deleted is a designed state, not an error: chat logs
// and published wiki pages keep their tokens forever, so the card degrades to
// a muted, non-clickable "whiteboard not found".
export function WhiteboardCard({ whiteboardRef }: { whiteboardRef: WhiteboardRef }) {
  const { t } = useTranslation('browse')
  const nav = useNav()
  // Hub isolation: the session (and every data route) is locked to nav's hub,
  // so a token minted in a DIFFERENT hub must not fetch — the server would only
  // 403 (hub_mismatch) — and can't be opened from here.
  const sameHub = nav.hubId !== null && whiteboardRef.hubId === nav.hubId
  const otherHub = nav.hubId !== null && !sameHub
  const q = useWhiteboard(whiteboardRef.projectId, whiteboardRef.boardId, !otherHub)
  const [open, setOpen] = useState(false)

  const board = q.data?.board ?? null
  // The list resolved but this board is not in it: deleted, or never existed.
  // An error (403 from a project the reader has no role in) is the weaker
  // "unavailable" — the card must not claim the board is gone.
  const gone = otherHub || (!!q.data && !board)
  const name = board?.name ?? whiteboardRef.name

  const subtitle = otherHub
    ? t('whiteboardCard.otherHub')
    : gone
      ? t('whiteboardCard.notFound')
      : board
        ? [
            board.projectName || whiteboardRef.projectName,
            t('whiteboardCard.updated', { date: fmtDate(board.updatedAt) }),
            board.updatedBy.name || board.updatedBy.email,
          ]
            .filter(Boolean)
            .join(' · ')
        : q.isLoading
          ? t('common:loading')
          : t('whiteboardCard.unavailable')

  const meta: CardMeta[] = board
    ? ([
        {
          label: t('card.modified'),
          value: fmtDate(board.updatedAt, { dateStyle: 'medium', timeStyle: 'short' }),
        },
        board.updatedBy.name ? { label: t('card.modifiedBy'), value: board.updatedBy.name } : null,
        { label: t('card.created'), value: fmtDate(board.createdAt, { dateStyle: 'medium' }) },
        { label: t('card.size'), value: fmtBytes(board.snapshotBytes) },
      ].filter(Boolean) as CardMeta[])
    : []

  return (
    <>
      <EntityCard
        title={board ? `${boardDisplayId(board)} ${name}` : name}
        subtitle={subtitle}
        icon={faChalkboard}
        meta={meta}
        metaLoading={q.isLoading}
        onNavigate={gone ? undefined : () => setOpen(true)}
        dimmed={gone}
        selectable
      />
      {open && <WhiteboardViewDialog whiteboardRef={whiteboardRef} onClose={() => setOpen(false)} />}
    </>
  )
}
