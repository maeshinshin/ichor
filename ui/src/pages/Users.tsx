import { useState } from 'preact/hooks'
import { EllipsisIcon, UsersIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ListToolbar } from '@/components/ListToolbar'
import { PageTitle } from '@/components/PageTitle'
import { SectionCard } from '@/components/SectionCard'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ToneBadge } from '@/components/ToneBadge'
import { CreateUserDialog, DeleteUserDialog, EditUserDialog, RevokeUserDialog, SetPasswordDialog } from '@/components/UserDialogs'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { userClient } from '@/lib/connect'
import { matchesQuery } from '@/lib/filter'
import { conditionTone } from '@/lib/conditions'
import { userError } from '@/lib/users'
import type { User } from '@/gen/ichor/v1alpha1/user_pb'
import { useList } from '@/lib/useList'
import { t } from '@/lib/i18n'

type Dialog = 'edit' | 'password' | 'revoke' | 'delete'

// The Users page (docs/adr/0094), in the sidebar only when ichor-ui has its own users. Every call
// is checked by Kubernetes RBAC as the caller, so a person without ichor-admin gets the 403 shown
// inline instead of a list.
export function Users() {
  const { items: users, error, reload } = useList(async () => (await userClient.listUsers({})).users, undefined, userError)
  const [query, setQuery] = useState('')

  if (error) return <ErrorAlert {...error} />
  if (!users) return <PageSkeleton label={t('users.loading')} />

  const shown = users.filter((u) => matchesQuery(query, u.name, u.displayName, u.email, ...u.roles))

  return (
    <div>
      <PageTitle title={t('users.title')} actions={<CreateUserDialog onDone={reload} />} />
      <ListToolbar query={query} onQuery={setQuery} shown={shown.length} total={users.length} onRefresh={reload} stock />
      {shown.length === 0 ? (
        <EmptyNote icon={UsersIcon}>{t('users.empty')}</EmptyNote>
      ) : (
        <SectionCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('users.colName')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('users.colDisplayName')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('users.colRoles')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('users.colDisabled')}</TableHead>
                <TableHead>{t('users.colReady')}</TableHead>
                <TableHead className="w-10">
                  <span class="sr-only">{t('col.actions')}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((u) => (
                <UserRow key={u.name} user={u} onChanged={reload} />
              ))}
            </TableBody>
          </Table>
        </SectionCard>
      )}
    </div>
  )
}

function UserRow({ user, onChanged }: { user: User; onChanged: () => void }) {
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const ready = user.conditions.find((c) => c.type === 'Ready')
  const close = (d: Dialog) => (open: boolean) => setDialog(open ? d : null)
  const roles =
    user.roles.length > 0 ? (
      <div class="flex flex-wrap gap-1">
        {user.roles.map((r) => (
          <Badge key={r} variant="secondary">
            {r}
          </Badge>
        ))}
      </div>
    ) : (
      t('common.none')
    )

  return (
    <TableRow>
      <TableCell className="max-sm:whitespace-normal max-sm:wrap-anywhere">
        {user.name}
        <div class="text-muted-foreground sm:hidden">{user.displayName || '-'}</div>
        <div class="mt-1 sm:hidden">{roles}</div>
        {user.disabled && (
          <div class="mt-1 sm:hidden">
            <Badge variant="destructive">{t('users.colDisabled')}</Badge>
          </div>
        )}
      </TableCell>
      <TableCell className="hidden sm:table-cell">{user.displayName || '-'}</TableCell>
      <TableCell className="hidden sm:table-cell">{roles}</TableCell>
      <TableCell className="hidden sm:table-cell">
        <Badge variant={user.disabled ? 'destructive' : 'outline'}>{user.disabled ? t('common.yes') : t('common.no')}</Badge>
      </TableCell>
      <TableCell>{ready ? <ToneBadge tone={conditionTone(ready.type, ready.status)}>{ready.reason || ready.status}</ToneBadge> : '-'}</TableCell>
      <TableCell>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={t('col.actions')}>
              <EllipsisIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-auto min-w-48">
            <DropdownMenuItem onSelect={() => setDialog('edit')}>{t('users.edit')}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDialog('password')}>{t('users.setPassword')}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDialog('revoke')}>{t('users.revoke')}</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => setDialog('delete')}>
              {t('users.delete')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {dialog === 'edit' && <EditUserDialog user={user} open onOpenChange={close('edit')} onDone={onChanged} />}
        {dialog === 'password' && <SetPasswordDialog user={user} open onOpenChange={close('password')} />}
        {dialog === 'revoke' && <RevokeUserDialog user={user} open onOpenChange={close('revoke')} />}
        {dialog === 'delete' && <DeleteUserDialog user={user} open onOpenChange={close('delete')} onDone={onChanged} />}
      </TableCell>
    </TableRow>
  )
}
