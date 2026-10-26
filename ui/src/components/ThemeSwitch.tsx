import { useState } from 'preact/hooks'
import { MoonIcon, SunIcon } from 'lucide-preact'
import { SidebarMenuButton } from '@/components/ui/sidebar'
import { applyTheme, currentTheme, saveTheme, type Theme } from '@/lib/theme'
import { useT } from '@/lib/useT'

// The light/dark switch, as a sidebar footer entry that stays
// an icon when the sidebar is collapsed. It names the mode it switches to.
export function ThemeSwitch() {
  const t = useT()
  const [theme, setTheme] = useState<Theme>(currentTheme)
  const next: Theme = theme === 'dark' ? 'light' : 'dark'
  const label = next === 'dark' ? t('theme.toDark') : t('theme.toLight')
  return (
    <SidebarMenuButton
      tooltip={label}
      onClick={() => {
        applyTheme(next)
        saveTheme(next)
        setTheme(next)
      }}
    >
      {next === 'dark' ? <MoonIcon /> : <SunIcon />}
      <span>{label}</span>
    </SidebarMenuButton>
  )
}
