// Logic behind the CA rotation credential hand-off on the operation page. Pure: no DOM,
// no preact, so it runs under `node --test`.

// Where the credentials panel is shown for an operation:
//   gate   the operation waits in AwaitingCredentials: download and continue.
//   issued the rotation went on (Issuing, Dropping, Finalizing): the old files no longer work, so a
//          person who continued without downloading can still get the new ones. The server allows the
//          download in exactly these stages and AwaitingCredentials (internal/uiapi/operation_credentials.go).
//   none   nothing to offer.
export type CredentialsPanel = 'gate' | 'issued' | 'none'

const issuedStages = ['Issuing', 'Dropping', 'Finalizing']

export function credentialsPanel(isCARotation: boolean, stage: string, finished: boolean): CredentialsPanel {
  if (!isCARotation || finished) return 'none'
  if (stage === 'AwaitingCredentials') return 'gate'
  return issuedStages.includes(stage) ? 'issued' : 'none'
}

// canContinue says whether "Continue rotation" is offered: only at the gate, and not again once this
// page has acknowledged it (the controller picks the annotation up on its next pass, so the stage
// stays AwaitingCredentials for a moment).
export function canContinue(panel: CredentialsPanel, acknowledged: boolean): boolean {
  return panel === 'gate' && !acknowledged
}

// canDownload says whether "Download new credentials" is offered: the panel is showing and the caller
// holds the download verb. An unknown permission (the probe has not answered, or failed) offers nothing.
export function canDownload(panel: CredentialsPanel, permitted: boolean | undefined): boolean {
  return panel !== 'none' && permitted === true
}
