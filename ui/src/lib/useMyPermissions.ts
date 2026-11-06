import { canListUsers } from '@/lib/actions'
import { diagnosticsClient } from '@/lib/connect'
import type { Permissions } from '@/lib/diagnostics'
import { useLoad } from '@/lib/useLoad'

type Ref = { namespace: string; name: string }

// useMyPermissions asks once for what the caller may download: the machine's
// downloads when a machine is given, the cluster's credentials when a cluster is. A page calls it
// once and passes the flags down. Undefined means not answered or the probe failed: offer nothing.
export function useMyPermissions(machine?: Ref, cluster?: Ref): Permissions | undefined {
  const { data } = useLoad(
    machine || cluster ? () => diagnosticsClient.getMyPermissions({ machine, cluster }) : undefined,
    [machine?.namespace, machine?.name, cluster?.namespace, cluster?.name],
  )
  return data ?? undefined
}

// useCanListUsers asks once whether the caller may list users, which decides if the nav offers the Users page.
// False until answered, and when the probe fails: offer nothing.
export function useCanListUsers(signedIn: boolean): boolean {
  return canListUsers(useLoad(signedIn ? () => diagnosticsClient.getMyPermissions({}) : undefined, [signedIn]).data ?? undefined)
}
