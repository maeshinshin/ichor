// What a successful write says in its toast. A write that became a pull
// request changed nothing yet, so it must not say "done" (docs/adr/0056).
export function outcomeMessage(res: { pullRequestUrl?: string }, done: string, pullRequest: string): string {
  return res.pullRequestUrl ? pullRequest : done
}
