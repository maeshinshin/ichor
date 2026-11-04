// Pure helpers for the Backups page.

type SnapshotLike = { name: string; time?: { seconds: bigint }; size: bigint }

// newestSnapshot is the snapshot with the latest time, or undefined when there is none. Its time is the
// backup's "last success": a snapshot is only stored after it was verified.
export function newestSnapshot<S extends Pick<SnapshotLike, 'time'>>(snapshots: readonly S[]): S | undefined {
  let best: S | undefined
  for (const s of snapshots) {
    if (!best || (s.time?.seconds ?? 0n) > (best.time?.seconds ?? 0n)) best = s
  }
  return best
}

// totalBytes sums the stored size of the snapshots.
export function totalBytes(snapshots: readonly SnapshotLike[]): number {
  return snapshots.reduce((n, s) => n + Number(s.size), 0)
}

// exampleBackupYaml is a starting TalosEtcdBackup for the cluster, shown when none exists.
export function exampleBackupYaml(cluster: { namespace: string; name: string }): string {
  return [
    'apiVersion: ichor.maesh.dev/v1alpha1',
    'kind: TalosEtcdBackup',
    'metadata:',
    `  name: ${cluster.name}`,
    `  namespace: ${cluster.namespace}`,
    'spec:',
    '  clusterRef:',
    `    name: ${cluster.name}`,
    '  schedule: "0 * * * *"',
    '  retention:',
    '    keepLast: 24',
    '  destination:',
    '    s3:',
    '      endpoint: https://s3.example.com',
    '      bucket: etcd-backups',
    `      prefix: ${cluster.name}`,
    '      credentialsSecretRef:',
    '        name: etcd-backup-s3',
    '',
  ].join('\n')
}

type DestinationLike = { endpoint: string; bucket: string; prefix: string; region?: string }

// destinationLabel is "bucket/prefix" on the endpoint; never anything about credentials.
export function destinationLabel(d: DestinationLike | undefined): string {
  if (!d) return '-'
  return `${d.bucket}/${d.prefix} @ ${d.endpoint}`
}

type BackupLike = { suspended: boolean; manualBackupPending: boolean }

// backNowBlock says why "Back up now" is unavailable, as an i18n key, or undefined when it is available.
// The server refuses the same two cases.
export function backNowBlock(b: BackupLike): 'backups.nowSuspended' | 'backups.nowPending' | undefined {
  if (b.suspended) return 'backups.nowSuspended'
  if (b.manualBackupPending) return 'backups.nowPending'
  return undefined
}
