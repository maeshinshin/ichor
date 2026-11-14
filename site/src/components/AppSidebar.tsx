import { ChevronRight, ExternalLink } from 'lucide-preact'
import { useState } from 'preact/hooks'
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
import { cn } from '@/lib/utils'

const contains = (n: NavNode, current: string): boolean => n.href === current || !!n.children?.some((c) => contains(c, current))

// A page is a link; `sub` indents it under the branch that holds it.
function Leaf({ node, current, sub }: { node: NavNode; current: string; sub?: boolean }) {
  const Button = sub ? SidebarMenuSubButton : SidebarMenuButton
  const item = (
    <Button asChild isActive={node.href === current}>
      <a href={node.href} title={node.label} aria-current={node.href === current ? 'page' : undefined}>
        <span>{node.label}</span>
      </a>
    </Button>
  )
  return sub ? <SidebarMenuSubItem>{item}</SidebarMenuSubItem> : <SidebarMenuItem>{item}</SidebarMenuItem>
}

// A node with children is one row, folding its contents: the page's own link beside a chevron, or for a directory with no
// page, the chevron row labelled with the directory. The contents are open when the page on screen is among them.
function Branch({ node, current, sub }: { node: NavNode; current: string; sub?: boolean }) {
  const self = node.children!.find((c) => c.href === node.page)
  const rest = node.children!.filter((c) => c !== self)
  const Button = sub ? SidebarMenuSubButton : SidebarMenuButton
  const Wrap = sub ? SidebarMenuSubItem : SidebarMenuItem
  // Controlled, as the On this page disclosure is: the chevron turns from the state, not from a group that nested rows share.
  // shortcut: the open state is set once, from the page on screen, because every navigation is a full load; add an effect on `current` if a client router is introduced, or the sidebar stops following the page.
  const [open, setOpen] = useState(() => contains(node, current))
  const chevron = (extra: string) => <ChevronRight className={cn('transition-transform', open && 'rotate-90', extra)} />
  return (
    <Wrap>
      {self && (
        <Button asChild isActive={self.href === current}>
          <a href={self.href} title={self.label} aria-current={self.href === current ? 'page' : undefined}>
            <span>{self.label}</span>
          </a>
        </Button>
      )}
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger asChild>
          {self ? (
            <SidebarMenuAction aria-label={`Show the pages under ${node.label}`}>{chevron('')}</SidebarMenuAction>
          ) : (
            <Button>
              <span>{node.label}</span>
              {chevron('ml-auto')}
            </Button>
          )}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <SidebarMenuSub>
            {rest.map((c) =>
              c.children ? (
                <Branch key={c.href ?? c.label} node={c} current={current} sub />
              ) : (
                <Leaf key={c.href} node={c} current={current} sub />
              ),
            )}
          </SidebarMenuSub>
        </CollapsibleContent>
      </Collapsible>
    </Wrap>
  )
}

// The category heading stays as it is; each page or directory under it is a row.
function Section({ node, current }: { node: NavNode; current: string }) {
  return (
    <SidebarGroup>
      {node.label && <SidebarGroupLabel>{node.label}</SidebarGroupLabel>}
      <SidebarGroupContent>
        <SidebarMenu>
          {node.children!.map((c) =>
            c.children ? (
              <Branch key={c.href ?? c.label} node={c} current={current} />
            ) : (
              <Leaf key={c.href} node={c} current={current} />
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
