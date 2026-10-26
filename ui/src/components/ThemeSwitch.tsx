import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { applyTheme, currentTheme, saveTheme, type Theme } from '@/lib/theme'

// The light/dark switch (docs/adr/0070). The button names the mode it switches to.
export function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>(currentTheme)
  const next: Theme = theme === 'dark' ? 'light' : 'dark'
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={() => {
        applyTheme(next)
        saveTheme(next)
        setTheme(next)
      }}
    >
      {next === 'dark' ? 'Dark mode' : 'Light mode'}
    </Button>
  )
}
