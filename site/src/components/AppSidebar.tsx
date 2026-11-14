import { ChevronRight, ExternalLink } from 'lucide-preact'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarFooter,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
} from '@/components/ui/sidebar'

import { VersionSwitcher } from '@/components/VersionSwitcher'
import type { NavNode } from '@/lib/docs'

// Every page below a node, in tree order.
const pages = (nodes: NavNode[]): NavNode[] => nodes.flatMap((n) => (n.children ? pages(n.children) : [n]))

const contains = (n: NavNode, current: string): boolean => n.href === current || !!n.children?.some((c) => contains(c, current))

function PageLink({ node, current }: { node: NavNode; current: string }) {
  return (
    <SidebarMenuButton asChild isActive={node.href === current}>
      <a href={node.href} title={node.label} aria-current={node.href === current ? 'page' : undefined}>
        <span>{node.label}</span>
      </a>
    </SidebarMenuButton>
  )
}

// A page with pages under it: the page stays listed, and the pages under it fold behind a chevron beside it. Open when the
// current page is one of them.
function Folded({ node, current }: { node: NavNode; current: string }) {
  const self = node.children!.find((c) => c.href === node.page) ?? node
  return (
    <SidebarMenuItem>
      <PageLink node={self} current={current} />
      <Collapsible defaultOpen={contains(node, current)} className="group/collapsible">
        <CollapsibleTrigger asChild>
          <SidebarMenuAction aria-label={`Show the pages under ${node.label}`}>
            <ChevronRight className="transition-transform group-data-[state=open]/collapsible:rotate-90" />
          </SidebarMenuAction>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <SidebarMenuSub>
            {pages(node.children!.filter((c) => c !== self)).map((c) => (
              <SidebarMenuSubItem key={c.href}>
                <SidebarMenuSubButton asChild isActive={c.href === current}>
                  <a href={c.href} title={c.label} aria-current={c.href === current ? 'page' : undefined}>
                    <span>{c.label}</span>
                  </a>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </CollapsibleContent>
      </Collapsible>
    </SidebarMenuItem>
  )
}

// The pages directly under a category are always listed, a directory's own pages included; the pages under one of them fold.
function Section({ node, current }: { node: NavNode; current: string }) {
  const items = node.children!.flatMap((c) => (c.children ? c.children : [c]))
  return (
    <SidebarGroup>
      {node.label && <SidebarGroupLabel>{node.label}</SidebarGroupLabel>}
      <SidebarGroupContent>
        <SidebarMenu>
          {items.map((c) =>
            c.children ? (
              <Folded key={c.href} node={c} current={current} />
            ) : (
              <SidebarMenuItem key={c.href}>
                <PageLink node={c} current={current} />
              </SidebarMenuItem>
            ),
          )}
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
