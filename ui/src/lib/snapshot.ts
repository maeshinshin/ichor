// refusalWithin waits up to `ms` for the snapshot frame's refusal text. A download fires no load event, so
// silence means it started (''), but the frame's load listener outlives the wait: a refusal after it (the server
// gives up on credentials after 20 s) goes to `late`, so it is never lost.
export async function refusalWithin(refused: Promise<string>, ms: number, late: (reason: string) => void): Promise<string> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const quiet = new Promise<null>((r) => (timer = setTimeout(() => r(null), ms)))
  const reason = await Promise.race([refused, quiet])
  clearTimeout(timer)
  if (reason === null) void refused.then((r) => r && late(r))
  return reason ?? ''
}

// refusalOf reads the frame's load events: appending an iframe fires a load for its initial about:blank document, which
// is not an answer; the first load of any other page is the server's refusal, and its text the reason. null: no answer yet.
export const refusalOf = (loads: { href: string; text: string }[]): string | null => loads.find((l) => l.href !== 'about:blank')?.text ?? null
