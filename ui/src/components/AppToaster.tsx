import { useEffect, useState } from 'preact/hooks'
import { Toaster } from '@/components/ui/sonner'
import { currentTheme, type Theme } from '@/lib/theme'

// The toaster follows the class on <html> that the theme switch sets, not the system preference.
export function AppToaster() {
  const [theme, setTheme] = useState<Theme>(currentTheme)
  useEffect(() => {
    const el = document.documentElement
    const sync = () => setTheme(el.classList.contains('dark') ? 'dark' : 'light')
    sync()
    const obs = new MutationObserver(sync)
    obs.observe(el, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])
  return <Toaster theme={theme} position="bottom-right" />
}
