import { ExternalLink, Moon, Search, Sun } from 'lucide-preact'
import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { AppSidebar } from '@/components/AppSidebar'
import { SearchDialog } from '@/components/SearchDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Kbd } from '@/components/ui/kbd'
import { Separator } from '@/components/ui/separator'
import { SidebarInset, SidebarProvider, SidebarTrigger, useSidebar } from '@/components/ui/sidebar'
import type { NavNode } from '@/lib/docs'

const sidebarKey = 'ichor-docs-sidebar'
const themeKey = 'ichor-docs-theme'

type Props = { nav: NavNode[]; current: string; base: string; version: string; repoUrl: string; children?: ComponentChildren }

function ThemeToggle() {
  const [dark, setDark] = useState(false)
  useEffect(() => setDark(document.documentElement.classList.contains('dark')), [])
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label="Toggle light and dark theme"
      onClick={() => {
        const next = !dark
        setDark(next)
        document.documentElement.classList.toggle('dark', next)
        try {
          localStorage.setItem(themeKey, next ? 'dark' : 'light')
        } catch {
          // The choice just does not outlive this tab.
        }
      }}
    >
      {dark ? <Sun /> : <Moon />}
    </Button>
  )
}

function Header({ base, version, repoUrl, onSearch }: Pick<Props, 'base' | 'version' | 'repoUrl'> & { onSearch: () => void }) {
  const { isMobile, open } = useSidebar()
  const [mac, setMac] = useState(false)
  useEffect(() => setMac(/mac|iphone|ipad/i.test(navigator.platform)), [])
  return (
    <header className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-2 border-b bg-background px-4">
      <SidebarTrigger />
      <Separator orientation="vertical" className="h-4 self-center" />
      {/* The brand lives in the sidebar; show it here while the sidebar is hidden or a drawer. */}
      {(isMobile || !open) && (
        <a href={base} className="flex items-center gap-2 font-semibold">
          Ichor
          <Badge variant="secondary">{version}</Badge>
        </a>
      )}
      <div className="ml-auto flex items-center gap-2">
        <Button variant="outline" className="md:w-64 md:justify-start" onClick={onSearch} aria-label="Search the docs">
          <Search />
          <span className="hidden text-muted-foreground md:inline">Search docs</span>
          <Kbd className="ml-auto hidden md:inline-flex">{mac ? '⌘K' : 'Ctrl K'}</Kbd>
        </Button>
        <Button asChild variant="ghost" className="hidden sm:inline-flex">
          <a href={repoUrl}>
            GitHub
            <ExternalLink />
          </a>
        </Button>
        <ThemeToggle />
      </div>
    </header>
  )
}

export function DocsShell({ nav, current, base, version, repoUrl, children }: Props) {
  const [open, setOpen] = useState(true)
  const [searching, setSearching] = useState(false)

  useEffect(() => {
    try {
      if (localStorage.getItem(sidebarKey) === 'closed') setOpen(false)
    } catch {
      // Storage blocked: the sidebar starts open.
    }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setSearching((s) => !s)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return (
    <SidebarProvider
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        try {
          localStorage.setItem(sidebarKey, v ? 'open' : 'closed')
        } catch {
          // The choice just does not outlive this tab.
        }
      }}
    >
      <AppSidebar nav={nav} current={current} base={base} version={version} repoUrl={repoUrl} />
      <SidebarInset>
        <Header base={base} version={version} repoUrl={repoUrl} onSearch={() => setSearching(true)} />
        {children}
      </SidebarInset>
      <SearchDialog base={base} open={searching} onOpenChange={setSearching} />
    </SidebarProvider>
  )
}
