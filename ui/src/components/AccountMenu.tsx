import { useState } from 'preact/hooks'
import { DownloadIcon, KeyRoundIcon, LogOutIcon, UserRoundIcon } from 'lucide-preact'
import { ChangePasswordDialog } from '@/components/ChangePassword'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
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
import { t } from '@/lib/i18n'

// The account menu next to the theme switch (docs/adr/0094): who the backend sees, change password
// (Ichor users only; an external IdP owns its own passwords), the caller's kubeconfig and sign out.
export function AccountMenu({ me }: { me: WhoAmIResponse }) {
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
          <DropdownMenuLabel>{t('account.signedInAs')}</DropdownMenuLabel>
          <DropdownMenuGroup>
            <div className="flex items-start gap-1.5 px-1.5 py-1 text-sm">
              <UserRoundIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <div className="grid min-w-0 flex-1 text-left leading-tight">
                <span className="truncate font-medium">{name}</span>
                {me.displayName && <span className="truncate text-xs text-muted-foreground">{me.username}</span>}
                <span className="truncate text-xs text-muted-foreground">
                  {t('account.roles')}: {roles.length > 0 ? roles.join(', ') : t('account.noRoles')}
                </span>
                <span className="truncate text-xs text-muted-foreground">
                  {t('account.source')}: {sourceLabel(me.source)}
                </span>
              </div>
            </div>
          </DropdownMenuGroup>
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
          <DropdownMenuLabel>{t('account.kubeconfigHint')}</DropdownMenuLabel>
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
