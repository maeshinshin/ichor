import { AppToaster } from '@/components/AppToaster'
import type { ComponentChildren } from 'preact'
import { Fragment } from 'preact'
import { ArchiveIcon, BoxesIcon, GitBranchIcon, LayoutDashboardIcon, LayersIcon, ListChecksIcon, ServerIcon, TriangleAlertIcon, UsersIcon } from 'lucide-preact'
import { AccountMenu } from '@/components/AccountMenu'
import type { WhoAmIResponse } from '@/gen/ichor/v1alpha1/user_pb'
import { IchorVersion } from '@/components/IchorVersion'
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
import { clusterHealth, type Health } from '@/lib/clusterHealth'
import { useCluster } from '@/lib/useCluster'
import { Badge } from '@/components/ui/badge'
import { Link } from '@/router'

const healthVariant: Record<Health, 'default' | 'destructive' | 'secondary'> = { ready: 'default', degraded: 'destructive', upgrading: 'secondary' }

// The cluster this Ichor manages: name, Talos and Kubernetes versions, health.
function ClusterBar() {
  const t = useT()
  const { cluster } = useCluster()
  if (!cluster) return null
  const health = clusterHealth(cluster)
  return (
    <div class="flex items-center gap-2 text-sm">
      <span class="font-medium">{cluster.ref?.name}</span>
      {cluster.talosVersion && <span class="hidden text-muted-foreground lg:inline">{t('shell.talos', { version: cluster.talosVersion })}</span>}
      {cluster.kubernetesVersion && <span class="hidden text-muted-foreground lg:inline">{t('shell.kubernetes', { version: cluster.kubernetesVersion })}</span>}
      <Badge variant={healthVariant[health]}>{t(`health.${health}`)}</Badge>
    </div>
  )
}

// Top-level nav. Node, operation and config detail pages are reached from a list row, not from
// here. There is no Clusters entry: the Overview is the cluster.
const navRoutes = [
  { path: '/', label: 'nav.overview', Icon: LayoutDashboardIcon, section: '' },
  { path: '/nodes', label: 'nav.machines', Icon: ServerIcon, section: '/nodes/' },
  { path: '/operations', label: 'nav.operations', Icon: ListChecksIcon, section: '/operations/' },
  { path: '/backups', label: 'nav.backups', Icon: ArchiveIcon, section: '' },
  { path: '/config', label: 'nav.config', Icon: LayersIcon, section: '/config/' },
  { path: '/gitops', label: 'nav.gitops', Icon: GitBranchIcon, section: '/gitops/' },
] satisfies { path: string; label: Key; Icon: typeof BoxesIcon; section: string }[]

// Shown only when ichor-ui has its own users.
const usersRoute: { path: string; label: Key; Icon: typeof BoxesIcon; section: string } = { path: '/users', label: 'nav.users', Icon: UsersIcon, section: '' }

// The nav entry is current on its list page and on the detail pages under it.
function isCurrent(r: { path: string; section: string }, path: string): boolean {
  return path === r.path || (r.section !== '' && path.startsWith(r.section))
}

function NavItems({ path, usersEnabled }: { path: string; usersEnabled: boolean }) {
  const t = useT()
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarMenu>
      {[...navRoutes, ...(usersEnabled ? [usersRoute] : [])].map((r) => (
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
  me,
  usersEnabled,
  children,
}: {
  path: string
  signInNotConfigured: boolean
  me: WhoAmIResponse | null
  usersEnabled: boolean
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
                <NavItems path={path} usersEnabled={usersEnabled} />
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
            <div className="ml-auto flex shrink-0 items-center gap-2">
              <ClusterBar />
              <IchorVersion />
              <ThemeSwitch />
              {me && <AccountMenu me={me} />}
            </div>
          </header>
          <div class="min-w-0 flex-1 p-4 sm:p-6">{children}</div>
          <AppToaster />
        </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
  )
}
