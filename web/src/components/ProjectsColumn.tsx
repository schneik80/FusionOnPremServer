import { useTranslation } from 'react-i18next'
import { useProjects } from '../api/queries'
import { useNav } from '../state/nav'
import { usePinToggle } from '../state/pins'
import { Column } from './Column'
import { ItemRow } from './ItemRow'

export function ProjectsColumn() {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const projectsQ = useProjects(nav.hubId)
  const { pinnedIds, toggle } = usePinToggle()

  const projects = projectsQ.data ?? []

  return (
    <Column
      title={t('projectsColumn.title')}
      flex={1}
      loading={projectsQ.isLoading}
      error={projectsQ.error as Error | null}
      empty={!projectsQ.isLoading && projects.length === 0}
      emptyText={nav.hubId ? t('projectsColumn.emptyInHub') : t('projectsColumn.selectHubHint')}
    >
      {projects.map((p) => (
        <ItemRow
          key={p.id}
          item={p}
          selected={nav.project?.id === p.id}
          onClick={() => nav.selectProject(p)}
          pinned={pinnedIds.has(p.id)}
          onTogglePin={toggle}
        />
      ))}
    </Column>
  )
}
