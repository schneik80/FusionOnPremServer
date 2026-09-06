import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, useSyncExternalStore } from 'react'
import { api } from '../api/client'
import { applyServerStatus, cooldownRemainingMs, getSnapshot, isHot, subscribe, type QuotaStatus } from './quota'

// useQuotaStatus is the one hook behind the slow-mode banner. The store is fed
// by headers on every response and by 429s; this hook only adds a poll of
// GET /api/quota while things are hot (for the countdown and the level), and
// a one-second ticker while a cooldown is running. Nothing polls when the
// budget is fine. If the server ever pushes this over SSE, only this file
// changes.
export function useQuotaStatus(): { slow: boolean; cooldownMs: number; status: QuotaStatus | null } {
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  const [now, setNow] = useState(() => Date.now())
  const hot = isHot(state, now)

  const q = useQuery({
    queryKey: ['quota'],
    queryFn: api.quota,
    enabled: hot,
    staleTime: 0,
    refetchInterval: hot ? 2000 : false,
    retry: false,
  })
  useEffect(() => {
    if (q.data) applyServerStatus(q.data)
  }, [q.data])

  const cooldownMs = cooldownRemainingMs(state, now)
  useEffect(() => {
    if (!hot) return
    const h = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(h)
  }, [hot])

  return { slow: hot, cooldownMs, status: state.server }
}
