import { useTranslation } from 'react-i18next'
import { HubBrowserDialog } from '../components/hubbrowser/HubBrowserDialog'
import type { HubPick } from '../components/hubbrowser/HubBrowserDialog'
import type { Item } from '../api/types'
import type { DocPin } from './types'

// AttachDocDialog reuses the hub browser (document mode) to pick an existing
// Fusion Team document, then hands the caller a DocPin the server can version-
// resolve. Upload-based supply (drag-drop) reuses the app's upload flow and is
// wired in a later phase; browsing the hub is the primary supply path and the
// one that exercises the full version-snapshot pipeline.
export function AttachDocDialog({
  open,
  hubId,
  initialProject,
  title,
  pickLabel,
  onClose,
  onPicked,
}: {
  open: boolean
  hubId: string | null
  initialProject: Item | null
  title?: string
  pickLabel?: string
  onClose: () => void
  onPicked: (pin: DocPin) => void
}) {
  const { t } = useTranslation('production')
  const handle = (pick: HubPick) => {
    if (!pick.item || !pick.project.altId) {
      onClose()
      return
    }
    onPicked({
      hubId: pick.hubId,
      itemId: pick.item.id,
      dmProjectId: pick.project.altId,
      name: pick.item.name,
      kind: pick.item.kind,
    })
    onClose()
  }

  return (
    <HubBrowserDialog
      open={open}
      hubId={hubId}
      title={title ?? t('attachDoc.title')}
      mode="document"
      selectable={(item) => !item.isContainer}
      initialProject={initialProject}
      pickLabel={pickLabel ?? t('attachDoc.attach')}
      onClose={onClose}
      onPick={handle}
    />
  )
}
