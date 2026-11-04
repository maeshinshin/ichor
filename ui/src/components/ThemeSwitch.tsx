import { Moon, Sun } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { applyTheme, currentTheme, saveTheme, type Theme } from '@/lib/theme'
import { t } from '@/lib/i18n'

// The light/dark switch, as an icon button at the right end of
// the top bar, the same as the preactcn site. Both icons are rendered and CSS
// picks one: the sun in dark mode (the one to switch to), the moon in light.
export function ThemeSwitch() {
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={t('theme.toggle')}
      onClick={() => {
        const next: Theme = currentTheme() === 'dark' ? 'light' : 'dark'
        applyTheme(next)
        saveTheme(next)
      }}
    >
      <Sun className="hidden dark:block" />
      <Moon className="dark:hidden" />
    </Button>
  )
}
