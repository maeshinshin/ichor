import type { ComponentChildren } from 'preact'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { t } from '@/lib/i18n'

// Every action that is hard to undo asks through this (preactcn alert-dialog;
// docs/adr/0083). It cannot be dismissed by a click outside, and focus starts on
// Cancel. The confirm button is a plain Button, not AlertDialogAction, so the
// dialog stays open when the server refuses and the error is shown inside it.
export function ConfirmDialog({
  open,
  onOpenChange,
  trigger,
  title,
  description,
  children,
  confirm,
  footer,
  closeLabel,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  trigger?: ComponentChildren
  title: string
  description?: string
  children?: ComponentChildren
  // confirm is the destructive button; omit it (with closeLabel) to show a
  // result that only needs closing.
  confirm?: { label: string; onClick: () => void; disabled?: boolean; destructive?: boolean }
  // footer replaces the whole footer (a multi-step dialog).
  footer?: ComponentChildren
  closeLabel?: string
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      {trigger && <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>}
      <AlertDialogContent className="grid-cols-1 data-[size=default]:max-w-[calc(100%-2rem)] data-[size=default]:sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
        </AlertDialogHeader>
        {children && <div className="-mx-4 max-h-[50dvh] overflow-y-auto px-4">{children}</div>}
        {footer ?? (
          <AlertDialogFooter>
            <AlertDialogCancel>{closeLabel ?? t('common.cancel')}</AlertDialogCancel>
            {confirm && (
              <Button variant={confirm.destructive === false ? 'default' : 'destructive'} disabled={confirm.disabled} onClick={confirm.onClick}>
                {confirm.label}
              </Button>
            )}
          </AlertDialogFooter>
        )}
      </AlertDialogContent>
    </AlertDialog>
  )
}
