import { useEffect, type DependencyList } from 'react'

const EVERY = 3000

/**
 * Runs load now, and again a few seconds after each run has finished, for
 * as long as the dependencies stay the same. A slow answer therefore delays
 * the next question instead of piling more on top of it. Nothing is asked
 * while the tab is hidden. load is told how to check it is still wanted.
 */
export function usePolling(load: (wanted: () => boolean) => Promise<void>, deps: DependencyList, repeat = true) {
  useEffect(() => {
    let current = true
    let busy = false
    let timer: number | undefined
    const run = async () => {
      if (document.hidden || busy) return
      busy = true
      window.clearTimeout(timer)
      try {
        await load(() => current)
      } finally {
        busy = false
        if (current && repeat) timer = window.setTimeout(run, EVERY)
      }
    }
    void run()
    document.addEventListener('visibilitychange', run)
    return () => {
      current = false
      window.clearTimeout(timer)
      document.removeEventListener('visibilitychange', run)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, repeat])
}
