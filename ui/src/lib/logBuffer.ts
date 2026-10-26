// Bounded, batched log state for the node log viewer (ADR 0079). A log
// stream can deliver thousands of lines at once (a kubelet's startup dump,
// a chatty etcd); rendering one DOM node and one state update per line is
// what kills a phone's tab. Lines go into a fixed-size ring, appended in
// batches, and long single messages are cut for display.

export interface Entry<T> {
  id: number
  item: T
}

// LogBuffer keeps the newest `capacity` items. Ids only grow, so a list
// keyed by id keeps its rows stable while old ones fall off the front.
export class LogBuffer<T> {
  private items: Entry<T>[] = []
  private nextId = 0

  private readonly capacity: number

  constructor(capacity: number) {
    this.capacity = capacity
  }

  push(batch: T[]): void {
    // Only the newest `capacity` of a huge batch can survive; a new array
    // each time so a state setter sees the change.
    const kept = batch.length > this.capacity ? batch.slice(-this.capacity) : batch
    this.nextId += batch.length - kept.length
    const added = kept.map((item) => ({ id: this.nextId++, item }))
    this.items = this.items.concat(added).slice(-this.capacity)
  }

  entries(): Entry<T>[] {
    return this.items
  }

  snapshot(): T[] {
    return this.items.map((e) => e.item)
  }

  clear(): void {
    this.items = []
  }
}

// truncateMessage cuts a message to `max` characters for the list; the full
// text stays in the entry for an expand control.
export function truncateMessage(message: string, max: number): { text: string; truncated: boolean } {
  if (message.length <= max) return { text: message, truncated: false }
  return { text: message.slice(0, max), truncated: true }
}

// createBatcher collects pushed items and hands them to `flush` in one call
// per scheduled tick. `schedule` runs a callback later and returns a cancel
// function (requestAnimationFrame / setTimeout in the page, a fake in tests).
export function createBatcher<T>(flush: (items: T[]) => void, schedule: (cb: () => void) => () => void) {
  let pending: T[] = []
  let cancelScheduled: (() => void) | null = null

  return {
    push(item: T): void {
      pending.push(item)
      if (cancelScheduled) return
      cancelScheduled = schedule(() => {
        cancelScheduled = null
        const items = pending
        pending = []
        flush(items)
      })
    },
    cancel(): void {
      pending = []
      cancelScheduled?.()
      cancelScheduled = null
    },
  }
}
