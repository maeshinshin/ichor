import { ExternalLink } from 'lucide-preact'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarFooter,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'

import { VersionSwitcher } from '@/components/VersionSwitcher'
import type { NavNode } from '@/lib/docs'

// Every page of a section is listed in it, in tree order: a directory's pages sit in the list, not behind a group.
const pages = (nodes: NavNode[]): NavNode[] => nodes.flatMap((n) => (n.children ? pages(n.children) : [n]))

function Section({ node, current }: { node: NavNode; current: string }) {
  return (
    <SidebarGroup>
      {node.label && <SidebarGroupLabel>{node.label}</SidebarGroupLabel>}
      <SidebarGroupContent>
        <SidebarMenu>
          {pages(node.children!).map((c) => (
            <SidebarMenuItem key={c.href}>
              <SidebarMenuButton asChild isActive={c.href === current}>
                <a href={c.href} title={c.label} aria-current={c.href === current ? 'page' : undefined}>
                  <span>{c.label}</span>
                </a>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

export function AppSidebar({ nav, current, base, root, version, repoUrl }: { nav: NavNode[]; current: string; base: string; root: string; version: string; repoUrl: string }) {
  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1">
          <a href={base} className="font-semibold">
            Ichor
          </a>
          <VersionSwitcher version={version} root={root} />
        </div>
      </SidebarHeader>
      <SidebarContent>
        {nav.map((n) => (
          <Section key={n.label} node={n} current={current} />
        ))}
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild>
              <a href={repoUrl}>
                <span>GitHub</span>
                <ExternalLink className="ml-auto" />
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
