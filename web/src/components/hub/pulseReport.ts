import type { ActivityReport, HubDayCount } from '../../api/types'

// The hub "pulse" reuses the existing isometric ActivityHeatmap, which buckets
// by per-event timestamp. So we expand a local-activity day series into
// synthetic events the heatmap can render unchanged — one event per counted
// change, at noon UTC that day. A busy quarter could mint thousands, so counts
// are scaled down to a ceiling; the heatmap shows relative shape, not exact
// tallies (HubPulse prints the true total beside it).
const MAX_SYNTH_EVENTS = 2400

export function synthReport(name: string, days: HubDayCount[]): ActivityReport {
  const nonEmpty = days.filter((d) => d.count > 0)
  const total = nonEmpty.reduce((s, d) => s + d.count, 0)
  const scale = total > MAX_SYNTH_EVENTS ? MAX_SYNTH_EVENTS / total : 1

  const events: ActivityReport['events'] = []
  for (const d of nonEmpty) {
    const n = scale < 1 ? Math.max(1, Math.round(d.count * scale)) : d.count
    const ts = `${d.day}T12:00:00Z`
    for (let i = 0; i < n; i++) {
      events.push({
        entityType: 'design',
        entityId: '',
        entityName: name,
        timestamp: ts,
        action: 'change',
        actor: { displayName: '' },
      })
    }
  }

  const sorted = nonEmpty.map((d) => d.day).sort()
  const createdOn = sorted.length ? `${sorted[0]}T00:00:00Z` : undefined
  const lastChange = sorted.length ? `${sorted[sorted.length - 1]}T23:59:59Z` : undefined

  return {
    scope: 'hub',
    scopeName: name,
    totalEvents: total,
    designCount: 0,
    versionCount: total,
    contributorCount: 0,
    createdOn,
    lastChange,
    bucket: 'day',
    timeline: nonEmpty.map((d) => ({ start: `${d.day}T00:00:00Z`, count: d.count })),
    contributors: [],
    children: [],
    events,
    eventsTruncated: scale < 1,
  }
}
