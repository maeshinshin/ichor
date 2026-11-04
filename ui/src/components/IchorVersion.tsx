import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { operationClient } from '@/lib/connect'
import { installerUnverified, versionLabel } from '@/lib/ichorVersion'
import { useLoad } from '@/lib/useLoad'
import { t } from '@/lib/i18n'

// The installed Ichor version in the top bar, with a second badge only when the cluster
// already knows a newer target (ADR 0059). It reads once per page load; a failed read just
// shows nothing, because the label is information, not a control.
export function IchorVersion() {
  const info = useLoad(() => operationClient.getIchorInstallation({}), []).data

  const label = versionLabel(info)
  if (!label) return null
  // An installer image run without its signature checked is said on every page (docs/adr/0122).
  const unverified = installerUnverified(info)
  return (
    <div class="flex items-center gap-2">
      <Badge variant="outline" aria-label={t('version.installed', { version: label.version })}>
        {label.version}
      </Badge>
      {unverified && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="destructive">{t('version.unverified')}</Badge>
          </TooltipTrigger>
          <TooltipContent>
            {unverified.digest !== '' ? t('version.unverifiedLast', { digest: unverified.digest, reason: unverified.reason }) : ''}
            {unverified.digest !== '' && unverified.pending ? ' ' : ''}
            {unverified.pending ? t('version.unverifiedNext') : ''}
          </TooltipContent>
        </Tooltip>
      )}
      {label.update && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="secondary">{t('version.updateAvailable')}</Badge>
          </TooltipTrigger>
          <TooltipContent>{t('version.updateHint', { version: label.update })}</TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}
