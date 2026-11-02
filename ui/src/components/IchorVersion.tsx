import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { operationClient } from '@/lib/connect'
import { versionLabel } from '@/lib/ichorVersion'
import { useLoad } from '@/lib/useLoad'
import { useT } from '@/lib/useT'

// The installed Ichor version in the top bar, with a second badge only when the cluster
// already knows a newer target (ADR 0059). It reads once per page load; a failed read just
// shows nothing, because the label is information, not a control.
export function IchorVersion() {
  const t = useT()
  const info = useLoad(() => operationClient.getIchorInstallation({}), []).data

  const label = versionLabel(info)
  if (!label) return null
  return (
    <div class="flex items-center gap-2">
      <Badge variant="outline" aria-label={t('version.installed', { version: label.version })}>
        {label.version}
      </Badge>
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
