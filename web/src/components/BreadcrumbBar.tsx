import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faChevronRight } from '@fortawesome/free-solid-svg-icons'
import { Box, Breadcrumbs, Link, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useNav } from '../state/nav'
import { HubIcon } from './entityIcons'

interface Crumb {
  label: string
  onClick?: () => void
  icon?: boolean
}

export function BreadcrumbBar({ onOpenHubs }: { onOpenHubs: () => void }) {
  const { t } = useTranslation('browse')
  const nav = useNav()

  const crumbs: Crumb[] = []
  // Inside a project the hub crumb navigates back to the hub's projects list
  // (clearProject); at the hub level it opens the hub switcher instead.
  if (nav.hubName)
    crumbs.push({ label: nav.hubName, onClick: nav.project ? nav.clearProject : onOpenHubs, icon: true })
  if (nav.project) crumbs.push({ label: nav.project.name, onClick: nav.gotoProjectRoot })
  nav.folderStack.forEach((f, i) =>
    crumbs.push({ label: f.name, onClick: () => nav.gotoFolder(i) }),
  )
  if (nav.selected) crumbs.push({ label: nav.selected.name })

  return (
    <Box
      sx={{
        px: 2,
        py: 1,
        borderBottom: 1,
        borderColor: 'divider',
        flexShrink: 0,
        minHeight: 41,
        display: 'flex',
        alignItems: 'center',
      }}
    >
      {crumbs.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('breadcrumb.noHub')}
        </Typography>
      ) : (
        <Breadcrumbs
          separator={
            <FontAwesomeIcon icon={faChevronRight} style={{ fontSize: 9, opacity: 0.5 }} />
          }
          sx={{ '& .MuiBreadcrumbs-li': { minWidth: 0 } }}
        >
          {crumbs.map((c, i) => {
            const isLast = i === crumbs.length - 1
            const content = (
              <Box component="span" sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
                {c.icon && <HubIcon style={{ fontSize: 11 }} />}
                {c.label}
              </Box>
            )
            if (isLast || !c.onClick) {
              return (
                <Typography key={i} variant="body2" color="text.primary" noWrap title={c.label}>
                  {content}
                </Typography>
              )
            }
            return (
              <Link
                key={i}
                component="button"
                variant="body2"
                underline="hover"
                color="text.secondary"
                onClick={c.onClick}
                sx={{ minWidth: 0 }}
                title={c.label}
              >
                {content}
              </Link>
            )
          })}
        </Breadcrumbs>
      )}
    </Box>
  )
}
