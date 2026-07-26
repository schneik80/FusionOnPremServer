import { Box, IconButton, Menu, MenuItem, Tooltip } from '@mui/material'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faArrowDownWideShort, faCloudArrowUp } from '@fortawesome/free-solid-svg-icons'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useFolderContents, useProjectContents } from '../api/queries'
import type { Item } from '../api/types'
import { useNav } from '../state/nav'
import { usePinToggle } from '../state/pins'
import { useUploads } from '../state/uploads'
import { Column } from './Column'
import { ItemRow } from './ItemRow'

type SortKey = 'name' | 'modified' | 'type'

const byName = (a: Item, b: Item) =>
  a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })

// typeOrder groups the contents list by document type for the "type" sort:
// folders first, then designs (assembly → part → plain), configured designs,
// drawings (dwg → template), and the ECAD kinds — mirroring typeTag's
// taxonomy. Subtypes are best-effort: a design whose assembly/part subtype
// isn't in the list data yet still lands with the other designs.
function typeOrder(item: Item): number {
  if (item.isContainer) return 0
  switch (item.kind) {
    case 'design':
      return item.subtype === 'assembly' ? 10 : item.subtype === 'part' ? 11 : 12
    case 'configured':
      return 20
    case 'drawing':
      return item.subtype === 'template' ? 31 : 30
    case 'schematic':
      return 40
    case 'pcb':
      return 50
    case 'ecad':
      return 60
    default:
      return 90
  }
}

// sortItems orders the contents list. "name" groups containers (folders) first,
// then alphabetises; "modified" is newest-first, with undated rows (folders)
// falling to the bottom by name; "type" groups by document kind, name within
// each group.
function sortItems(list: Item[], sort: SortKey): Item[] {
  const arr = [...list]
  if (sort === 'modified') {
    arr.sort((a, b) => {
      const am = a.modifiedOn ? Date.parse(a.modifiedOn) : -Infinity
      const bm = b.modifiedOn ? Date.parse(b.modifiedOn) : -Infinity
      return am !== bm ? bm - am : byName(a, b)
    })
  } else if (sort === 'type') {
    arr.sort((a, b) => {
      const at = typeOrder(a)
      const bt = typeOrder(b)
      return at !== bt ? at - bt : byName(a, b)
    })
  } else {
    arr.sort((a, b) => {
      if (a.isContainer !== b.isContainer) return a.isContainer ? -1 : 1
      return byName(a, b)
    })
  }
  return arr
}

function SortMenu({ value, onChange }: { value: SortKey; onChange: (v: SortKey) => void }) {
  const { t } = useTranslation('browse')
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const pick = (v: SortKey) => {
    onChange(v)
    setAnchor(null)
  }
  return (
    <>
      <Tooltip title={t('contents.sort')}>
        <IconButton
          size="small"
          aria-label={t('contents.sortAria')}
          onClick={(e) => setAnchor(e.currentTarget)}
          sx={{ color: 'text.secondary' }}
        >
          <FontAwesomeIcon icon={faArrowDownWideShort} style={{ fontSize: 12 }} />
        </IconButton>
      </Tooltip>
      <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
        <MenuItem selected={value === 'name'} onClick={() => pick('name')}>
          {t('contents.sortName')}
        </MenuItem>
        <MenuItem selected={value === 'modified'} onClick={() => pick('modified')}>
          {t('contents.sortModified')}
        </MenuItem>
        <MenuItem selected={value === 'type'} onClick={() => pick('type')}>
          {t('contents.sortType')}
        </MenuItem>
      </Menu>
    </>
  )
}

export function ContentsColumn() {
  const { t } = useTranslation('browse')
  const nav = useNav()
  const { pinnedIds, toggle } = usePinToggle()
  const uploads = useUploads()
  const [sort, setSort] = useState<SortKey>('name')

  const atRoot = nav.folderStack.length === 0

  // At a project root, contents come from the combined folders+items endpoint;
  // inside a folder, from the folder-contents endpoint. The inactive query is
  // disabled by passing a null id.
  const rootQ = useProjectContents(atRoot ? (nav.project?.id ?? null) : null)
  const folderQ = useFolderContents(nav.hubId, atRoot ? null : nav.currentFolderId)

  const activeQ = atRoot ? rootQ : folderQ
  const list: Item[] = atRoot
    ? [...(rootQ.data?.folders ?? []), ...(rootQ.data?.items ?? [])]
    : (folderQ.data ?? [])
  const sorted = sortItems(list, sort)

  const onRowClick = (item: Item) => {
    if (item.isContainer) nav.enterFolder(item)
    else nav.selectItem(item)
  }

  return (
    <Column
      title={t('contents.title')}
      flex={1}
      action={
        uploads.target || list.length > 0 ? (
          <Box sx={{ display: 'flex', alignItems: 'center' }}>
            {uploads.target && (
              <Tooltip title={t('contents.uploadHere')}>
                <IconButton
                  size="small"
                  aria-label={t('contents.uploadAria')}
                  onClick={uploads.openDialog}
                  sx={{ color: 'text.secondary' }}
                >
                  <FontAwesomeIcon icon={faCloudArrowUp} style={{ fontSize: 12 }} />
                </IconButton>
              </Tooltip>
            )}
            {list.length > 0 && <SortMenu value={sort} onChange={setSort} />}
          </Box>
        ) : undefined
      }
      loading={activeQ.isLoading}
      error={activeQ.error as Error | null}
      empty={!activeQ.isLoading && list.length === 0}
      emptyText={nav.project ? t('contents.emptyFolder') : t('contents.selectProject')}
    >
      {sorted.map((item) => (
        <ItemRow
          key={item.id}
          item={item}
          selected={nav.selected?.id === item.id}
          onClick={() => onRowClick(item)}
          pinned={pinnedIds.has(item.id)}
          onTogglePin={toggle}
          classifyEnabled
        />
      ))}
    </Column>
  )
}
