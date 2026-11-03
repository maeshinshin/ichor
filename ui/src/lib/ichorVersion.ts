// What the version label in the top bar shows. Pure: no DOM, no preact.

export type IchorVersionInfo = {
  installedVersion: string
  targetVersion: string
  updateAvailable: boolean
}

export type VersionLabel = {
  // version is the installed version, shown as is (it already starts with "v").
  version: string
  // update is the newer version the cluster knows, or null. It is set only when the server
  // says an update is available and names a version, never guessed from the two strings.
  update: string | null
}

// versionLabel is null when there is nothing honest to show: no IchorInstallation, or the
// controller has not reported its version yet.
export function versionLabel(info: IchorVersionInfo | null): VersionLabel | null {
  if (!info || info.installedVersion === '') return null
  return {
    version: info.installedVersion,
    update: info.updateAvailable && info.targetVersion !== '' ? info.targetVersion : null,
  }
}

// releaseNotice is the newest release the controller found in the chart repository when it is newer than
// the running version, or null. A failed check shows nothing here; Settings says why.
export function releaseNotice(info: { latestVersion: string; releaseAvailable: boolean } | null): string | null {
  if (!info || !info.releaseAvailable || info.latestVersion === '') return null
  return info.latestVersion
}
