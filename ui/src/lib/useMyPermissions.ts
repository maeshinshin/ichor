import { useEffect, useState } from 'preact/hooks'
import { diagnosticsClient } from '@/lib/connect'
import { toPermissions, type Permissions } from '@/lib/diagnostics'

type Ref = { namespace: string; name: string }

// useMyPermissions asks once for what the caller may download: the machine's
// downloads when a machine is given, the cluster's credentials when a cluster is. A page calls it
// once and passes the flags down. Undefined means not answered or the probe failed: offer nothing.
export function useMyPermissions(machine?: Ref, cluster?: Ref): Permissions | undefined {
  const [perms, setPerms] = useState<Permissions | undefined>()

  useEffect(() => {
    let cancelled = false
    setPerms(undefined)
    if (machine || cluster) {
      diagnosticsClient.getMyPermissions({ machine, cluster }).then(
        (res) => {
          if (!cancelled) setPerms(toPermissions(res))
        },
        () => {},
      )
    }
    return () => {
      cancelled = true
    }
  }, [machine?.namespace, machine?.name, cluster?.namespace, cluster?.name])

  return perms
}
