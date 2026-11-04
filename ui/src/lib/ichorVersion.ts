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

// installerUnverified says when the installer image of an Ichor upgrade runs without its signature checked: the last upgrade did (verification Skipped), or the next one will (the spec says skip). It is
// null when nothing was or will be skipped, so the UI stays quiet for the normal, verified case.
export type InstallerUnverified = {
  // reason is the reason the person gave, or '' when only the next upgrade is known to skip (an unrecorded older run).
  reason: string
  // digest is the installer image the last upgrade ran, or ''.
  digest: string
  // pending is true when the next upgrade will skip the check, whether or not the last one did.
  pending: boolean
}

export function installerUnverified(
  info: { installerVerification: string; installerSkipReason: string; installerDigest: string; installerSignatureSkip: boolean } | null,
): InstallerUnverified | null {
  if (!info) return null
  const last = info.installerVerification === 'Skipped'
  if (!last && !info.installerSignatureSkip) return null
  return { reason: last ? info.installerSkipReason : '', digest: last ? info.installerDigest : '', pending: info.installerSignatureSkip }
}
