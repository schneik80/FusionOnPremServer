import { describe, expect, it } from 'vitest'
import { readDmDeepLink, resolvedPermalink } from './dmDeepLink'
import { searchToNav } from './navUrl'

// The deep link is a one-shot launcher: it must be recognized only when BOTH
// ids are present (one alone resolves nothing), and the permalink it turns
// into must parse back as the same place — that round trip is what lets
// NavProvider take over without knowing DM ids exist.

const resolved = {
  hubId: 'urn:hub:h1',
  hubName: 'Acme',
  projectId: 'urn:proj:p1',
  projectName: 'Robot Arm',
  projectAltId: 'a.proj1',
}

describe('readDmDeepLink', () => {
  it('reads both ids and the name hint', () => {
    expect(readDmDeepLink('?dmHubId=a.hub1&dmProjectId=a.proj1&projectName=Robot%20Arm')).toEqual({
      dmHubId: 'a.hub1',
      dmProjectId: 'a.proj1',
      projectName: 'Robot Arm',
    })
  })

  it('accepts a search string without the leading ?', () => {
    expect(readDmDeepLink('dmHubId=a.hub1&dmProjectId=a.proj1')?.dmProjectId).toBe('a.proj1')
  })

  it('defaults the name hint to empty', () => {
    expect(readDmDeepLink('?dmHubId=a.hub1&dmProjectId=a.proj1')?.projectName).toBe('')
  })

  it('is null without both ids', () => {
    expect(readDmDeepLink('?dmHubId=a.hub1')).toBeNull()
    expect(readDmDeepLink('?dmProjectId=a.proj1')).toBeNull()
    expect(readDmDeepLink('')).toBeNull()
    expect(readDmDeepLink('?hub=urn:hub:h1~Acme')).toBeNull()
  })
})

describe('resolvedPermalink', () => {
  it('renders a hub+project permalink that parses back', () => {
    const url = resolvedPermalink(resolved)
    expect(url.startsWith('/?')).toBe(true)
    const nav = searchToNav(url.slice(1))
    expect(nav.hubId).toBe(resolved.hubId)
    expect(nav.hubName).toBe(resolved.hubName)
    expect(nav.project?.id).toBe(resolved.projectId)
    expect(nav.project?.name).toBe(resolved.projectName)
    expect(nav.folderStack).toEqual([])
    expect(nav.selected).toBeNull()
  })

  it('carries no DM ids into the rewritten URL', () => {
    expect(readDmDeepLink(resolvedPermalink(resolved).slice(1))).toBeNull()
  })
})
