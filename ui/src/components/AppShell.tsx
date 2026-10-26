import { AppToaster } from '@/components/AppToaster'
import type { ComponentChildren } from 'preact'
import { Fragment } from 'preact'
import { BoxesIcon, GitBranchIcon, LayoutDashboardIcon, LayersIcon, ListChecksIcon, ServerIcon, TriangleAlertIcon } from 'lucide-preact'
import { ThemeSwitch } from '@/components/ThemeSwitch'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { Separator } from '@/components/ui/separator'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { breadcrumbsFor } from '@/lib/breadcrumbs'
import type { Key } from '@/lib/i18n'
import { usePageTitles } from '@/lib/usePageTitles'
import { useT } from '@/lib/useT'
import { Link } from '@/router'

// Top-level nav. Cluster detail (/clusters/:namespace/:name), node detail
// (/nodes/:namespace/:name) and patch edit (/patches/:namespace/:name) are
// reached from a list row, not from here, same as there's no nav entry for
// an individual GitHub PR page.
const navRoutes = [
  { path: '/', label: 'nav.overview', Icon: LayoutDashboardIcon, section: '' },
  { path: '/clusters', label: 'nav.clusters', Icon: BoxesIcon, section: '/clusters/' },
  { path: '/machines', label: 'nav.machines', Icon: ServerIcon, section: '/nodes/' },
  { path: '/patches', label: 'nav.patches', Icon: LayersIcon, section: '/patches/' },
  { path: '/operations', label: 'nav.operations', Icon: ListChecksIcon, section: '/operations/' },
  { path: '/gitops', label: 'nav.gitops', Icon: GitBranchIcon, section: '/gitops/' },
] satisfies { path: string; label: Key; Icon: typeof BoxesIcon; section: string }[]

// The nav entry is current on its list page and on the detail pages under it.
function isCurrent(r: { path: string; section: string }, path: string): boolean {
  return path === r.path || (r.section !== '' && path.startsWith(r.section))
}

function NavItems({ path }: { path: string }) {
  const t = useT()
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarMenu>
      {navRoutes.map((r) => (
        <SidebarMenuItem key={r.path}>
          <SidebarMenuButton asChild isActive={isCurrent(r, path)} tooltip={t(r.label)}>
            <Link to={r.path} onClick={() => setOpenMobile(false)}>
              <r.Icon />
              <span>{t(r.label)}</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      ))}
    </SidebarMenu>
  )
}

function Crumbs({ path }: { path: string }) {
  const t = useT()
  const titles = usePageTitles()
  const crumbs = breadcrumbsFor(path, titles)
  return (
    <Breadcrumb aria-label={t('nav.breadcrumb')} className="min-w-0">
      <BreadcrumbList className="flex-nowrap">
        {crumbs.map((c, i) => {
          const text = c.label ? t(c.label) : c.text
          return (
            <Fragment key={i}>
              {i > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem className="min-w-0">
                {c.to ? (
                  <BreadcrumbLink asChild>
                    <Link to={c.to}>{text}</Link>
                  </BreadcrumbLink>
                ) : (
                  <BreadcrumbPage className="truncate">{text}</BreadcrumbPage>
                )}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}

// AppShell is the Argo CD / console style frame: a left sidebar that collapses
// to icons on desktop and becomes a sheet on a phone (the sidebar component
// does this below 768px), and a sticky top bar with the breadcrumb. The
// content column has min-w-0 so a wide table scrolls inside itself and the page
// never gets a horizontal scrollbar.
export function AppShell({
  path,
  signInNotConfigured,
  children,
}: {
  path: string
  signInNotConfigured: boolean
  children: ComponentChildren
}) {
  const t = useT()
  return (
    <TooltipProvider>
      <SidebarProvider>
        <Sidebar collapsible="icon">
          <SidebarHeader>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton asChild size="lg" tooltip="Ichor">
                  <Link to="/">
                    <span class="flex size-8 shrink-0 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
                      <BoxesIcon class="size-4" />
                    </span>
                    <span class="text-base font-semibold">Ichor</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarHeader>
          <SidebarContent>
            <SidebarGroup>
              <SidebarGroupLabel>{t('nav.menu')}</SidebarGroupLabel>
              <SidebarGroupContent>
                <NavItems path={path} />
              </SidebarGroupContent>
            </SidebarGroup>
          </SidebarContent>
          <SidebarFooter>
            <SidebarMenu>
              {signInNotConfigured && (
                <SidebarMenuItem>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <SidebarMenuButton className="text-amber-600 dark:text-amber-400">
                        <TriangleAlertIcon />
                        <span>{t('app.noLoginTitle')}</span>
                      </SidebarMenuButton>
                    </TooltipTrigger>
                    <TooltipContent side="right" className="max-w-xs">
                      {t('app.noLoginBody')}
                    </TooltipContent>
                  </Tooltip>
                </SidebarMenuItem>
              )}
            </SidebarMenu>
          </SidebarFooter>
          <SidebarRail />
        </Sidebar>
        <SidebarInset className="min-w-0">
          <header class="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-2 border-b bg-background/95 px-4 backdrop-blur">
            <SidebarTrigger className="-ml-1" />
            <Separator orientation="vertical" className="mr-1 h-4" />
            <Crumbs path={path} />
            <div className="ml-auto shrink-0">
              <ThemeSwitch />
            </div>
          </header>
          <div class="min-w-0 flex-1 p-4 sm:p-6">{children}</div>
          <AppToaster />
        </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
  )
}
