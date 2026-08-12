import { faArrowUpRightFromSquare, faBoxArchive, faCirclePlus, faDownload } from '@fortawesome/free-solid-svg-icons'
import { useTranslation } from 'react-i18next'
import { api } from '../../api/client'
import type { Item } from '../../api/types'
import { FUSION_NATIVE_KINDS, INSERTABLE_KINDS } from '../../api/types'
import { useFusionActions } from '../../state/fusionActions'
import type { CardAction } from '../entitycard/EntityCard'

// The document actions a data card offers, in one place.
//
// These are the same three verbs as the details header (DocumentActions.tsx) —
// Open, Insert, Archive — plus the plain download an uploaded file already had.
// The cards had only the download, which was the wrong half: a card in a chat
// message or on a production step is exactly where someone wants to say "get
// this into Fusion", and they were being sent back to the browser to find the
// document again first.
//
// The rule the card cannot get wrong is which of Archive and Download it shows.
// A Fusion-native design has NO downloadable storage object of its own — the
// old download button was offered enabled and simply failed — so for native
// kinds the byte-level action is Archive: ask APS to build an F3Z/F3D, and let
// the bell hand it over when it lands. Uploaded files keep the direct link.
//
// The icons are deliberately distinct from Navigate's location arrow: Open
// leaves for another application, Navigate moves within this one.

export interface DocActionTarget {
  /** lineage urn */
  itemId: string
  name: string
  /** the RESOLVED kind (see kindFromTypename) — a stale hint mis-gates these */
  kind: string
  /**
   * True while `kind` is still only a hint and the details query that would
   * confirm it is in flight. Every action here is gated on nativeness, so
   * acting on a hint would offer a design the download that cannot work — the
   * exact bug this replaced. The bar simply waits; it never guesses.
   */
  kindPending?: boolean
  /** the project's DM id; without it nothing here can be addressed */
  dmProjectId?: string
  /**
   * Pins the archive to one version. A production card passes the version it
   * froze so the file matches its v{n} badge; everything else omits it and
   * archives the tip.
   */
  versionId?: string
}

/**
 * useDocActions builds the Open / Insert / Archive-or-Download actions for one
 * document card. Returns an empty list when the document can't be addressed
 * (no DM project id, or a card minted in another hub) — a button that always
 * fails is worse than no button.
 */
export function useDocActions(target: DocActionTarget | null): CardAction[] {
  const { t } = useTranslation('browse')
  const { runAction, pendingFor, startArchive, archiveFor } = useFusionActions()

  // Hooks first, always: the caller decides whether the card is addressable,
  // and that decision must not change how many hooks run.
  if (!target?.dmProjectId || target.kindPending) return []
  const { itemId, name, kind, dmProjectId, versionId } = target

  // Fusion is addressed by lineage, so the item it needs is the lineage — a
  // pinned card's Open lands on the current version, which is Fusion's own
  // behaviour and is what the labels below say.
  const item: Item = { id: itemId, name, kind, isContainer: false }
  const native = FUSION_NATIVE_KINDS.has(kind)
  const pending = pendingFor(itemId)
  const archiving = archiveFor(itemId, versionId)

  const actions: CardAction[] = []

  if (native) {
    actions.push({
      key: 'open',
      icon: faArrowUpRightFromSquare,
      label: t('card.openInFusion'),
      busy: pending === 'open',
      disabled: pending !== null,
      onClick: () => void runAction(item, dmProjectId, 'open'),
    })
    if (INSERTABLE_KINDS.has(kind)) {
      actions.push({
        key: 'insert',
        icon: faCirclePlus,
        label: t('card.insertInFusion'),
        busy: pending === 'insert',
        disabled: pending !== null,
        onClick: () => void runAction(item, dmProjectId, 'insert'),
      })
    }
    // Native: no storage object to link to, so this starts a job instead. It
    // outlives the card — the notification bell is where it is collected.
    actions.push({
      key: 'archive',
      icon: faBoxArchive,
      label: archiving ? t('card.archiving') : t('card.archive'),
      busy: !!archiving,
      disabled: !!archiving,
      onClick: () => void startArchive(item, dmProjectId, versionId),
    })
    return actions
  }

  // Uploaded file: real bytes at a real url, handed to the browser directly.
  actions.push({
    key: 'download',
    icon: faDownload,
    label: t('card.download'),
    href: api.downloadUrl(dmProjectId, itemId, name),
    download: true,
  })
  return actions
}
