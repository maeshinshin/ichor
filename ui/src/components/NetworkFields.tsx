import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { LinkChoice, NetworkChoice, NetworkForm } from '@/lib/machineSettings'
import { t, type Key } from '@/lib/i18n'

const DEFAULT = 'default'

// The primary network card and its addressing, shared with nothing else yet: the approval form
// owns the state (see networkChoice in lib/machineSettings for turning it into the settings sent).
export function NetworkFields({
  idPrefix,
  links,
  form,
  preselected,
  errors,
  onChange,
}: {
  idPrefix: string
  // links already filtered to those that can be picked (selectableLinks).
  links: LinkChoice[]
  // preselected is true while the form starts on the default card (defaultLink), not one the person picked.
  preselected?: boolean
  form: NetworkForm
  errors: NetworkChoice['errors']
  onChange: (f: NetworkForm) => void
}) {
  const set = (patch: Partial<NetworkForm>) => onChange({ ...form, ...patch })
  const err = (k: Key | undefined) => k && <p class="text-destructive">{t(k)}</p>

  return (
    <section class="space-y-2">
      <h4 class="font-medium">{t('network.title')}</h4>
      <div class="space-y-1">
        <Label htmlFor={`${idPrefix}-nic`}>{t('network.nic')}</Label>
        {links.length > 0 ? (
          <Select value={form.mac || DEFAULT} onValueChange={(v) => set({ mac: v === DEFAULT ? '' : v })}>
            <SelectTrigger id={`${idPrefix}-nic`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={DEFAULT}>{t('network.nicDefault')}</SelectItem>
              {links.map((l) => (
                <SelectItem key={l.mac} value={l.mac}>
                  {l.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            id={`${idPrefix}-nic`}
            value={form.mac}
            placeholder="aa:bb:cc:dd:ee:ff"
            onInput={(e) => set({ mac: (e.target as HTMLInputElement).value })}
            aria-invalid={!!errors.mac}
          />
        )}
        {err(errors.mac)}
        <p class="text-muted-foreground">
          {links.length === 0 ? t('network.nicTyped') : preselected ? t('network.nicPreselected') : t('network.nicHelp')}
        </p>
      </div>

      {form.mac !== '' && (
        <>
          <div class="space-y-1">
            <Label htmlFor={`${idPrefix}-addressing`}>{t('network.addressing')}</Label>
            <Select value={form.addressing} onValueChange={(v) => set({ addressing: v as NetworkForm['addressing'] })}>
              <SelectTrigger id={`${idPrefix}-addressing`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="dhcp">{t('network.dhcp')}</SelectItem>
                <SelectItem value="static">{t('network.static')}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {form.addressing === 'static' && (
            <>
              <div class="space-y-1">
                <Label htmlFor={`${idPrefix}-addresses`}>{t('network.addresses')}</Label>
                <Input
                  id={`${idPrefix}-addresses`}
                  value={form.addresses}
                  placeholder="192.168.1.10/24"
                  onInput={(e) => set({ addresses: (e.target as HTMLInputElement).value })}
                  aria-invalid={!!errors.addresses}
                />
                {err(errors.addresses)}
                <p class="text-muted-foreground">{t('network.addressesHelp')}</p>
              </div>
              <div class="space-y-1">
                <Label htmlFor={`${idPrefix}-gateway`}>{t('network.gateway')}</Label>
                <Input
                  id={`${idPrefix}-gateway`}
                  value={form.gateway}
                  placeholder="192.168.1.1"
                  onInput={(e) => set({ gateway: (e.target as HTMLInputElement).value })}
                  aria-invalid={!!errors.gateway}
                />
                {err(errors.gateway)}
              </div>
            </>
          )}

          <div class="space-y-1">
            <Label htmlFor={`${idPrefix}-dns`}>{t('network.dns')}</Label>
            <Input
              id={`${idPrefix}-dns`}
              value={form.dns}
              placeholder="1.1.1.1, 9.9.9.9"
              onInput={(e) => set({ dns: (e.target as HTMLInputElement).value })}
              aria-invalid={!!errors.dns}
            />
            {err(errors.dns)}
            <p class="text-muted-foreground">{t('network.dnsHelp')}</p>
          </div>

          <div class="space-y-1">
            <Label htmlFor={`${idPrefix}-vlan`}>{t('network.vlan')}</Label>
            <Input
              id={`${idPrefix}-vlan`}
              inputMode="numeric"
              value={form.vlan}
              onInput={(e) => set({ vlan: (e.target as HTMLInputElement).value })}
              aria-invalid={!!errors.vlan}
            />
            {err(errors.vlan)}
            <p class="text-muted-foreground">{t('network.vlanHelp')}</p>
          </div>
        </>
      )}
    </section>
  )
}
