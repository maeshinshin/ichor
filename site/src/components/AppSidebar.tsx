import { ChevronRight, ExternalLink } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
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
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
} from '@/components/ui/sidebar'

import type { NavNode } from '@/lib/docs'

const contains = (n: NavNode, current: string): boolean => n.href === current || !!n.children?.some((c) => contains(c, current))

function Sub({ node, current }: { node: NavNode; current: string }) {
  if (!node.children) {
    return (
      <SidebarMenuSubItem>
        <SidebarMenuSubButton asChild isActive={node.href === current}>
          <a href={node.href} title={node.label} aria-current={node.href === current ? 'page' : undefined}>
            <span>{node.label}</span>
          </a>
        </SidebarMenuSubButton>
      </SidebarMenuSubItem>
    )
  }
  return (
    <SidebarMenuSubItem>
      <Branch node={node} current={current} />
    </SidebarMenuSubItem>
  )
}

// A directory inside a section: a collapsible list, open when it holds the current page.
function Branch({ node, current }: { node: NavNode; current: string }) {
  return (
    <Collapsible defaultOpen={!node.collapsed || contains(node, current)} className="group/collapsible">
      <CollapsibleTrigger asChild>
        <SidebarMenuButton>
          <span>{node.label}</span>
          <ChevronRight className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-90" />
        </SidebarMenuButton>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <SidebarMenuSub>
          {node.children!.map((c) => (
            <Sub key={c.href ?? c.label} node={c} current={current} />
          ))}
        </SidebarMenuSub>
      </CollapsibleContent>
    </Collapsible>
  )
}

function Section({ node, current }: { node: NavNode; current: string }) {
  const items = (
    <SidebarGroupContent>
      <SidebarMenu>
        {node.children!.map((c) =>
          c.children ? (
            <SidebarMenuItem key={c.label}>
              <Branch node={c} current={current} />
            </SidebarMenuItem>
          ) : (
            <SidebarMenuItem key={c.href}>
              <SidebarMenuButton asChild isActive={c.href === current}>
                <a href={c.href} title={c.label} aria-current={c.href === current ? 'page' : undefined}>
                  <span>{c.label}</span>
                </a>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ),
        )}
      </SidebarMenu>
    </SidebarGroupContent>
  )
  if (!node.collapsed) {
    return (
      <SidebarGroup>
        {node.label && <SidebarGroupLabel>{node.label}</SidebarGroupLabel>}
        {items}
      </SidebarGroup>
    )
  }
  // A long section (the decision records) starts closed unless the current page is in it.
  return (
    <Collapsible defaultOpen={contains(node, current)} className="group/collapsible">
      <SidebarGroup>
        <SidebarGroupLabel asChild>
          <CollapsibleTrigger>
            {node.label}
            <ChevronRight className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-90" />
          </CollapsibleTrigger>
        </SidebarGroupLabel>
        <CollapsibleContent>{items}</CollapsibleContent>
      </SidebarGroup>
    </Collapsible>
  )
}

export function AppSidebar({ nav, current, base, version, repoUrl }: { nav: NavNode[]; current: string; base: string; version: string; repoUrl: string }) {
  return (
    <Sidebar>
      <SidebarHeader>
        <a href={base} className="flex items-center gap-2 px-2 py-1 font-semibold">
          Ichor
          <Badge variant="secondary">{version}</Badge>
        </a>
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
