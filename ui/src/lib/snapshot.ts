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
