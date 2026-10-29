import type { OpState } from './operations.ts'

// How a status is drawn: good (green check), bad (red cross), warn (amber
// triangle), progress (spinner), neutral (grey dot).
export type Tone = 'good' | 'bad' | 'warn' | 'progress' | 'neutral'

export function opStateTone(state: OpState): Tone {
  switch (state) {
    case 'done':
      return 'good'
    case 'failed':
      return 'bad'
    case 'running':
      return 'progress'
    case 'paused':
      return 'warn'
    default:
      return 'neutral'
  }
}
