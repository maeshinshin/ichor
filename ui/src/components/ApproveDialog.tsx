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
import { ErrorAlert } from '@/components/ErrorAlert'
import { DiskRoleFields } from '@/components/DiskRoleFields'
import { InstallDiskPicker } from '@/components/InstallDiskPicker'
import { NetworkFields } from '@/components/NetworkFields'
import type { NodeDetail } from '@/gen/ichor/v1alpha1/node_pb'
import { MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { machineClient, nodeClient } from '@/lib/connect'
import { notifyWarning, notifyWrite } from '@/lib/notify'
import { describeError, type ErrorShape } from '@/lib/errors'
import { canApprove, isStrongIdentity } from '@/lib/identity'
import {
  defaultInstallDisk,
  diskKey,
  diskSelector,
  installChoice,
  isValidHostname,
  type DiskOption,
} from '@/lib/installDisk'
import { formatBytes } from '@/lib/format'
import type { TalosLink } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import {
  defaultLink,
  diskRolesChoice,
  networkChoice,
  selectableLinks,
  type DiskRolesForm,
  type NetworkForm,
  type NicOption,
} from '@/lib/machineSettings'
import { useT } from '@/lib/useT'

const emptyNetwork: NetworkForm = { mac: '', addressing: 'dhcp', addresses: '', gateway: '', dns: '', vlan: '' }
const emptyRoles: DiskRolesForm = { ephemeral: '', volumes: [] }

// ApproveDialog approves one pending node: role, hostname, install disk, network card and other disk roles.
// Used by the node list and by the cluster's Add node page.
export function ApproveDialog({
  machine,
  onApproved,
}: {
  machine: Machine
  onApproved: (m: Machine) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [node, setNode] = useState<NodeDetail | null>(null)
  const [nodeError, setNodeError] = useState<string | null>(null)
  const [role, setRole] = useState<MachineRole>(MachineRole.UNSPECIFIED)
  const [hostname, setHostname] = useState(machine.hostname)
  // installKey is diskKey of the chosen disk, '' for none, or null while the person has not chosen:
  // then the default disk applies (defaultInstallDisk). installDev is the typed device path, used
  // only while neither discovery nor the node reports a disk.
  const [installKey, setInstallKey] = useState<string | null>(null)
  const [installDev, setInstallDev] = useState('')
  const [installImage, setInstallImage] = useState('')
  // links are the node's own NICs, read only while discovery recorded none.
  const [links, setLinks] = useState<TalosLink[]>([])
  // networkForm is null until the person changes it, so the default NIC applies (defaultLink).
  const [networkForm, setNetworkForm] = useState<NetworkForm | null>(null)
  const [rolesForm, setRolesForm] = useState<DiskRolesForm>(emptyRoles)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)

  const strong = isStrongIdentity(machine.identity)
  const hostnameOk = isValidHostname(hostname)
  // The disks and NICs discovery recorded are the list to choose from; the node's own
  // reads fill in for a machine discovered before they were recorded.
  const disks: DiskOption[] = (
    machine.discoveredDisks.length > 0 ? machine.discoveredDisks : (node?.disks ?? [])
  ).filter((d) => diskSelector(d))
  const nics: NicOption[] = machine.discoveredLinks.length > 0 ? machine.discoveredLinks : links
  const linkChoices = selectableLinks(nics, machine.identity?.macAddresses ?? [])
  const defaultDisk = defaultInstallDisk(disks)
  const defaultNic = defaultLink(linkChoices)
  const shownInstallKey = installKey ?? (defaultDisk ? diskKey(defaultDisk) : '')
  const shownNetwork: NetworkForm = networkForm ?? { ...emptyNetwork, mac: defaultNic?.mac ?? '' }
  const net = networkChoice(shownNetwork)
  const roles = diskRolesChoice(rolesForm, shownInstallKey)
  // Without a list (the Talos API unreachable, or no disk reported) the person can still type a device path.
  const { install, ok: installOk } = installChoice({ disks, key: shownInstallKey, typed: installDev, image: installImage })
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
    setNetworkForm(null)
    setRolesForm(emptyRoles)
    setRole(MachineRole.UNSPECIFIED)
    setHostname(machine.hostname)
    setInstallKey(null)
    setInstallDev('')
    setInstallImage('')
    setReason('')
    setSubmitError(null)
  }, [open])


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
      if (res.machine) onApproved(res.machine)
      notifyWrite('toast.approved', { name: hostname || machine.ref.name })
      // Approved anyway: a pick discovery did not see is only worth a note.
      for (const w of res.warnings) notifyWarning(w)
      setOpen(false)
    } catch (err) {
      setSubmitError(describeError(err))
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
            value={shownInstallKey}
            defaulted={installKey === null && defaultDisk !== undefined}
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
            form={shownNetwork}
            preselected={networkForm === null && defaultNic !== undefined}
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
