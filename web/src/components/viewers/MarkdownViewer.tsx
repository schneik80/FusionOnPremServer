import { useTranslation } from 'react-i18next'
import { Markdown } from '../../wiki/Markdown'
import type { ViewerFile } from './kind'
import { FallbackViewer, ViewerSpinner } from './ui'
import { useFileText } from './useFileText'

// MarkdownViewer renders an uploaded .md file with the same GitHub-flavoured
// renderer the Wiki uses, so a standalone markdown file reads like a wiki page.
export function MarkdownViewer({
  file,
  dmProjectId,
  itemId,
}: {
  file: ViewerFile
  dmProjectId: string
  itemId: string
}) {
  const { t } = useTranslation('browse')
  const { loading, text, tooLarge, error } = useFileText(dmProjectId, itemId)

  if (loading) return <ViewerSpinner />
  if (error) return <FallbackViewer file={file} reason={error} />
  if (tooLarge) return <FallbackViewer file={file} reason={t('viewer.tooLarge')} />
  return <Markdown>{text}</Markdown>
}
