import { useEffect, useState } from 'preact/hooks'
import { EllipsisIcon } from 'lucide-preact'
import { ErrorAlert } from '@/components/ErrorAlert'
import { DataTable, ListToolbar } from '@/components/ListToolbar'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { CreateUserDialog, DeleteUserDialog, EditUserDialog, RevokeUserDialog, SetPasswordDialog } from '@/components/UserDialogs'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { User } from '@/gen/ichor/v1alpha1/user_pb'
import { userClient } from '@/lib/connect'
import { matchesQuery } from '@/lib/filter'
import { conditionStatusTone } from '@/lib/status'
import { userError } from '@/lib/users'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }
type Dialog = 'edit' | 'password' | 'revoke' | 'delete'

// The Users page, in the sidebar only when ichor-ui has its own users. Every call
// is checked by Kubernetes RBAC as the caller, so a person without ichor-admin gets the 403 shown
// inline instead of a list.
export function Users() {
  const t = useT()
  const [users, setUsers] = useState<User[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [query, setQuery] = useState('')

  async function reload() {
    try {
      setUsers((await userClient.listUsers({})).users)
      setError(null)
    } catch (err) {
      setError(userError(err))
    }
  }

  useEffect(() => {
    reload()
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!users) return <PageSkeleton label={t('users.loading')} />

  const shown = users.filter((u) => matchesQuery(query, u.name, u.displayName, u.email, ...u.roles))

  return (
    <div class="space-y-6">
      <PageHeader title={t('users.title')} actions={<CreateUserDialog onDone={reload} />} />
      <ListToolbar query={query} onQuery={setQuery} shown={shown.length} total={users.length} onRefresh={reload} />
      {shown.length === 0 ? (
        <EmptyNote>{t('users.empty')}</EmptyNote>
      ) : (
        <DataTable>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('users.colName')}</TableHead>
                <TableHead>{t('users.colDisplayName')}</TableHead>
                <TableHead>{t('users.colRoles')}</TableHead>
                <TableHead>{t('users.colDisabled')}</TableHead>
                <TableHead>{t('users.colReady')}</TableHead>
                <TableHead class="w-10">
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
        </DataTable>
      )}
    </div>
  )
}

function UserRow({ user, onChanged }: { user: User; onChanged: () => void }) {
  const t = useT()
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const ready = user.conditions.find((c) => c.type === 'Ready')
  const close = (d: Dialog) => (open: boolean) => setDialog(open ? d : null)

  return (
    <TableRow>
      <TableCell class="font-medium">{user.name}</TableCell>
      <TableCell>{user.displayName || '-'}</TableCell>
      <TableCell>{user.roles.length > 0 ? user.roles.join(', ') : t('common.none')}</TableCell>
      <TableCell>{user.disabled ? t('common.yes') : t('common.no')}</TableCell>
      <TableCell>{ready ? <StatusText tone={conditionStatusTone(ready.type, ready.status)}>{ready.reason || ready.status}</StatusText> : '-'}</TableCell>
      <TableCell>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={t('col.actions')}>
              <EllipsisIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
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
