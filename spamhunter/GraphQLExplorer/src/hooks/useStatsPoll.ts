// useStatsPoll — seconds-scale stats poll with Page Visibility pause and a
// maxLevId-diff "corpus changed" nudge flag (STATS-02).
//
// Source: RESEARCH § "useStatsPoll with hidden-tab pause + maxLevId diff" /
// Architecture Pattern 4 (poll-and-diff); contract §9 (poll maxLevId on a sane
// seconds interval — there is NO push/subscription); CONTEXT § "Locked from
// Research". MDN Page Visibility API for hidden-tab pause.
//
// Discipline (Pitfall: aggressive polling):
//   - setTimeout RESCHEDULE (NOT a fixed repeating timer) — a slow request can
//     never stack overlapping polls, and a hidden tab cleanly skips work and
//     reschedules.
//   - Seconds-scale interval (default 5000ms, exposed as the tunable
//     POLL_INTERVAL_MS constant) — never sub-second.
//   - A strict maxLevId increase flips the `hasNewData` NUDGE FLAG ONLY. It NEVER
//     auto-refetches any investigation window — the analyst decides (acknowledge /
//     Refresh stats). In this phase there is no window yet, but the discipline is
//     structural so Phases 2–4 inherit it.
import { useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { StatsDocument } from '../queries/stats.graphql'
import { classify, type ApiError } from '../transport/errors'

/**
 * Default poll interval (ms). Seconds-scale per contract §9 / RESEARCH A3 — any
 * 2–10s value satisfies "seconds-scale". Exposed as a named tunable constant.
 */
export const POLL_INTERVAL_MS = 5000

export interface Stats {
  eventCount: number
  maxLevId: number
  dbVersion: number
  pinnedStrfryVersion: string
}

export interface UseStatsPoll {
  /** Latest successfully-classified stats, or undefined before the first load. */
  stats: Stats | undefined
  /** The classified error of the most recent tick, or null when clean. */
  error: ApiError | null
  /** True until the first tick resolves (initial load). */
  loading: boolean
  /** Nudge flag — flips true on a strict maxLevId increase; NEVER auto-refetches. */
  hasNewData: boolean
  /** Resets the nudge flag (dismiss the nudge). */
  acknowledge: () => void
  /** True while the poll is paused because the tab is hidden (Page Visibility). */
  isPaused: boolean
  /** Force an immediate re-pull on demand (the "Refresh stats" CTA). */
  refresh: () => void
}

/**
 * Pure nudge decision (extracted for unit testing — see useStatsPoll.test.ts).
 * The corpus-changed nudge flips ONLY when the newly-observed maxLevId strictly
 * increases versus the last seen value. The first observation (no prior value)
 * never nudges; an unchanged or (defensively) decreasing value never nudges.
 */
export function shouldNudge(lastLevId: number | null, nextLevId: number): boolean {
  return lastLevId !== null && nextLevId > lastLevId
}

export function useStatsPoll(intervalMs: number = POLL_INTERVAL_MS): UseStatsPoll {
  const [stats, setStats] = useState<Stats>()
  const [error, setError] = useState<ApiError | null>(null)
  const [loading, setLoading] = useState(true)
  const [hasNewData, setHasNewData] = useState(false)
  const [isPaused, setIsPaused] = useState(
    typeof document !== 'undefined' && document.visibilityState === 'hidden',
  )

  const lastLevId = useRef<number | null>(null)
  // tickRef lets the refresh() callback trigger an out-of-band immediate poll
  // without re-running the effect (which would tear down/rebuild the timer).
  const tickRef = useRef<() => void>(() => {})

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    let cancelled = false

    const schedule = () => {
      if (!cancelled) timer = setTimeout(tick, intervalMs)
    }

    const tick = async () => {
      if (cancelled) return
      // Page Visibility pause: skip the network call on a hidden tab and
      // reschedule (no work on hidden tab). Mirrors isPaused for the view.
      if (document.visibilityState === 'hidden') {
        setIsPaused(true)
        schedule()
        return
      }
      setIsPaused(false)

      // urql's toPromise() normally RESOLVES with a CombinedError on the result
      // (classify() handles it), but a defect/throw in the exchange chain (or a
      // custom exchange added in Phases 2–4) could REJECT the promise. Without a
      // catch, the rejection skips schedule() below and the poll loop dies
      // permanently (WR-04). Wrap so a throw still classifies + reschedules — the
      // type is inferred from the typed StatsDocument, so data access stays typed.
      const result = await client
        .query(StatsDocument, {})
        .toPromise()
        .catch(() => 'THREW' as const)
      if (cancelled) return
      if (result === 'THREW') {
        // Genuine transport/exchange throw — treat as NETWORK and keep polling.
        setError({ kind: 'NETWORK' })
        setLoading(false)
        schedule()
        return
      }

      const apiError = classify(result)
      if (apiError) {
        setError(apiError)
        setLoading(false)
        schedule()
        return
      }

      const s = result.data?.stats
      if (s) {
        // Strict increase → flip the nudge flag ONLY. Never auto-refetch.
        if (shouldNudge(lastLevId.current, s.maxLevId)) setHasNewData(true)
        lastLevId.current = s.maxLevId
        setStats(s)
        setError(null)
      }
      setLoading(false)
      schedule()
    }

    tickRef.current = () => {
      if (cancelled) return
      clearTimeout(timer)
      void tick()
    }

    // Resume immediately when the tab becomes visible again (don't wait a full
    // interval); on hide, surface the paused signal without issuing a request.
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        setIsPaused(false)
        clearTimeout(timer)
        void tick()
      } else {
        setIsPaused(true)
      }
    }
    document.addEventListener('visibilitychange', onVisibility)

    void tick()

    return () => {
      cancelled = true
      clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [intervalMs])

  return {
    stats,
    error,
    loading,
    hasNewData,
    acknowledge: () => setHasNewData(false),
    isPaused,
    // refresh re-pulls on demand AND acknowledges the nudge (the analyst chose to update).
    refresh: () => {
      setHasNewData(false)
      tickRef.current()
    },
  }
}
