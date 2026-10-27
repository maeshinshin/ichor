import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { DiskRoleFields } from '@/components/DiskRoleFields'
import { InstallDiskPicker } from '@/components/InstallDiskPicker'
import { NetworkFields } from '@/components/NetworkFields'
import type { NodeDetail } from '@/gen/ichor/v1alpha1/node_pb'
import { MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { machineClient, nodeClient } from '@/lib/connect'
import { offersBreakGlass } from '@/lib/breakGlass'
import { notifyWrite } from '@/lib/notify'
import { describeError } from '@/lib/errors'
import { canApprove, isStrongIdentity } from '@/lib/identity'
import { diskSelector, installChoice, isValidHostname } from '@/lib/installDisk'
import { formatBytes } from '@/lib/format'
import type { TalosLink } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import {
  diskRolesChoice,
  networkChoice,
  selectableLinks,
  type DiskRolesForm,
  type NetworkForm,
} from '@/lib/machineSettings'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

const emptyNetwork: NetworkForm = { mac: '', addressing: 'dhcp', addresses: '', gateway: '', dns: '', vlan: '' }
const emptyRoles: DiskRolesForm = { ephemeral: '', volumes: [] }

// ApproveDialog approves one pending node: role, hostname, install disk, network card and other disk roles.
// Used by the node list and by the cluster's Add node page.
export function ApproveDialog({
  machine,
  onApproved,
  onPullRequest,
}: {
  machine: Machine
  onApproved: (m: Machine) => void
  onPullRequest: (url: string, run: BreakGlassRun) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [node, setNode] = useState<NodeDetail | null>(null)
  const [nodeError, setNodeError] = useState<string | null>(null)
  const [role, setRole] = useState<MachineRole>(MachineRole.UNSPECIFIED)
  const [hostname, setHostname] = useState(machine.hostname)
  // installKey is diskKey of the chosen disk, or '' for none; installDev is the typed device
  // path used only while discovery reports no disks.
  const [installKey, setInstallKey] = useState('')
  const [installDev, setInstallDev] = useState('')
  const [installImage, setInstallImage] = useState('')
  const [links, setLinks] = useState<TalosLink[]>([])
  const [networkForm, setNetworkForm] = useState<NetworkForm>(emptyNetwork)
  const [rolesForm, setRolesForm] = useState<DiskRolesForm>(emptyRoles)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)
  const [refused, setRefused] = useState(false)

  const strong = isStrongIdentity(machine.identity)
  const hostnameOk = isValidHostname(hostname)
  const disks = (node?.disks ?? []).filter((d) => diskSelector(d))
  const linkChoices = selectableLinks(links, machine.identity?.macAddresses ?? [])
  const net = networkChoice(networkForm)
  const roles = diskRolesChoice(rolesForm, installKey)
  // The list comes from the node itself; without it (the Talos API unreachable, or no disk
  // reported) the person can still type a device path.
  const { install, ok: installOk } = installChoice({ disks, key: installKey, typed: installDev, image: installImage })
  const canSubmit = canApprove(machine.identity, role !== MachineRole.UNSPECIFIED) && hostnameOk && installOk && net.ok && roles.ok

  useEffect(() => {
    if (!open || !machine.ref) return
    setNode(null)
    setNodeError(null)
    nodeClient.getNode({ machine: machine.ref }).then(
      (res) => setNode(res.node ?? null),
      (err) => setNodeError(describeError(err).detail),
    )
    // The network cards come from a separate read; without them the MAC can be typed.
    setLinks([])
    nodeClient.getNodeTalosDetails({ machine: machine.ref }).then(
      (res) => setLinks(res.network?.links ?? []),
      () => setLinks([]),
    )
    // Reset the form each time the dialog is reopened.
    setNetworkForm(emptyNetwork)
    setRolesForm(emptyRoles)
    setRole(MachineRole.UNSPECIFIED)
    setHostname(machine.hostname)
    setInstallKey('')
    setInstallDev('')
    setInstallImage('')
    setReason('')
    setSubmitError(null)
    setRefused(false)
  }, [open])

  // The same approval again with break_glass set.
  const breakGlassApprove: BreakGlassRun = async (bg) => {
    const res = await machineClient.approveMachine({
      machine: machine.ref,
      role,
      hostname,
      install,
      network: net.network,
      disks: roles.disks,
      nodeLabels: {},
      reason,
      breakGlass: bg,
    })
    if (res.machine) onApproved(res.machine)
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }

  async function submit() {
    if (!machine.ref || !canSubmit) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await machineClient.approveMachine({
        machine: machine.ref,
        role,
        hostname,
        install,
        network: net.network,
        disks: roles.disks,
        nodeLabels: {},
        reason,
      })
      if (res.pullRequestUrl) onPullRequest(res.pullRequestUrl, breakGlassApprove)
      else if (res.machine) onApproved(res.machine)
      notifyWrite(res, 'toast.approved', { name: hostname || machine.ref.name })
      setOpen(false)
    } catch (err) {
      setSubmitError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">{t('approve.button')}</Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('approve.title', { name: machine.ref?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('approve.description')}</DialogDescription>
        </DialogHeader>

        <div class="space-y-3 text-sm">
          <section>
            <h4 class="font-medium">{t('approve.identity')}</h4>
            <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-muted-foreground">
              <dt>{t('approve.uuid')}</dt>
              <dd class="text-foreground">{machine.identity?.uuid || t('approve.missing')}</dd>
              <dt>{t('approve.serial')}</dt>
              <dd class="text-foreground">{machine.identity?.serial || t('approve.missing')}</dd>
              <dt>{t('approve.mac')}</dt>
              <dd class="text-foreground">
                {machine.identity?.macAddresses.length ? machine.identity.macAddresses.join(', ') : t('common.none')}
              </dd>
            </dl>
            {!strong && (
              <p class="mt-2 rounded-md border border-destructive/30 bg-destructive/10 p-2 text-destructive">
                {t('approve.weakIdentity')}
              </p>
            )}
          </section>

          <section>
            <h4 class="font-medium">{t('approve.foundAddress')}</h4>
            <p class="text-muted-foreground">{machine.endpoint || t('approve.noAddress')}</p>
          </section>

          <section>
            <h4 class="font-medium">{t('approve.hardware')}</h4>
            {nodeError && <p class="text-muted-foreground">{t('approve.hardwareFailed', { detail: nodeError })}</p>}
            {!nodeError && !node && <p class="text-muted-foreground">{t('common.loading')}</p>}
            {node?.hardware && (
              <p class="text-muted-foreground">
                {t('approve.hardwareSummary', {
                  manufacturer: node.hardware.manufacturer,
                  model: node.hardware.model,
                  cores: node.hardware.cpuCores,
                  ram: formatBytes(node.hardware.memoryTotalBytes),
                })}
              </p>
            )}
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-role">{t('approve.role')}</Label>
            <Select value={role ? String(role) : ''} onValueChange={(v) => setRole(Number(v) as MachineRole)}>
              <SelectTrigger id="approve-role" className="w-full">
                <SelectValue placeholder={t('approve.selectRole')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={String(MachineRole.CONTROL_PLANE)}>{t('role.controlPlane')}</SelectItem>
                <SelectItem value={String(MachineRole.WORKER)}>{t('role.worker')}</SelectItem>
              </SelectContent>
            </Select>
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-hostname">{t('approve.hostname')}</Label>
            <Input
              id="approve-hostname"
              value={hostname}
              onInput={(e) => setHostname((e.target as HTMLInputElement).value)}
              aria-invalid={!hostnameOk}
            />
            {!hostnameOk && <p class="text-destructive">{t('approve.hostnameInvalid')}</p>}
          </section>

          <InstallDiskPicker
            idPrefix="approve"
            disks={disks}
            value={installKey}
            typed={installDev}
            image={installImage}
            typedOk={installOk}
            showImage={!!install}
            onValue={setInstallKey}
            onTyped={setInstallDev}
            onImage={setInstallImage}
          />

          <DiskRoleFields
            idPrefix="approve"
            disks={disks}
            form={rolesForm}
            errors={roles.errors}
            onChange={setRolesForm}
          />

          <NetworkFields
            idPrefix="approve"
            links={linkChoices}
            form={networkForm}
            errors={net.errors}
            onChange={setNetworkForm}
          />

          <section class="space-y-1">
            <Label htmlFor="approve-reason">{t('approve.reason')}</Label>
            {/* Symmetric with reject's reason: approving rarely needs a justification -
                identity, hardware and role above already say what's being approved -
                but one can still be recorded, e.g. "replacing node-3, same role". */}
            <Textarea
              id="approve-reason"
              value={reason}
              onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
              placeholder={t('approve.reasonPlaceholder')}
            />
          </section>

          {submitError && <ErrorAlert {...submitError} />}
          {refused && <BreakGlassDialog run={breakGlassApprove} onDone={() => setOpen(false)} />}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button disabled={!canSubmit || submitting} onClick={submit}>
            {submitting ? t('approve.submitting') : t('approve.button')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
