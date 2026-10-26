// What a successful write says in its toast. A write that became a pull
// request changed nothing yet, so it must not say "done".
export function outcomeMessage(res: { pullRequestUrl?: string }, done: string, pullRequest: string): string {
  return res.pullRequestUrl ? pullRequest : done
}
