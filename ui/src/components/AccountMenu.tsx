import { useState } from 'preact/hooks'
import { DownloadIcon, KeyRoundIcon, LogOutIcon, UserRoundIcon } from 'lucide-preact'
import { ChangePasswordDialog } from '@/components/ChangePassword'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { WhoAmIResponse } from '@/gen/ichor/v1alpha1/user_pb'
import { downloadKubeconfig } from '@/lib/download'
import { notifyDone, notifyFailed } from '@/lib/notify'
import { signOut } from '@/lib/oidc'
import { rolesOf, sourceLabel, userError } from '@/lib/users'
import { useT } from '@/lib/useT'

// The account menu next to the theme switch: who the backend sees, change password
// (Ichor users only; an external IdP owns its own passwords), the caller's kubeconfig and sign out.
export function AccountMenu({ me }: { me: WhoAmIResponse }) {
  const t = useT()
  const [passwordOpen, setPasswordOpen] = useState(false)
  const roles = rolesOf(me.groups)
  const name = me.displayName || me.username

  async function kubeconfig() {
    try {
      notifyDone(t('account.kubeconfigSaved', { name: await downloadKubeconfig() }))
    } catch (err) {
      notifyFailed(t('account.kubeconfigFailed'), userError(err).detail)
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={t('account.menu')}>
            <UserRoundIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuLabel class="space-y-0.5 font-normal">
            <p class="text-xs text-muted-foreground">{t('account.signedInAs')}</p>
            <p class="break-words text-sm font-medium">{name}</p>
            {me.displayName && <p class="break-words text-xs text-muted-foreground">{me.username}</p>}
            <p class="text-xs text-muted-foreground">
              {t('account.roles')}: {roles.length > 0 ? roles.join(', ') : t('account.noRoles')}
            </p>
            <p class="text-xs text-muted-foreground">
              {t('account.source')}: {sourceLabel(me.source)}
            </p>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {me.source === 'ichor' && (
            <DropdownMenuItem onSelect={() => setPasswordOpen(true)}>
              <KeyRoundIcon />
              {t('account.changePassword')}
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={kubeconfig}>
            <DownloadIcon />
            {t('account.kubeconfig')}
          </DropdownMenuItem>
          <p class="px-2 pb-1.5 text-xs text-muted-foreground">{t('account.kubeconfigHint')}</p>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={signOut}>
            <LogOutIcon />
            {t('account.signOut')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ChangePasswordDialog open={passwordOpen} onOpenChange={setPasswordOpen} />
    </>
  )
}
