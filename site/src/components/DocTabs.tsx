import { render } from 'preact'
import { useEffect } from 'preact/hooks'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

/**
 * Makes each `:::tabs` block of the article interactive (plugins/docs.mjs, remarkTabs): the preactcn Tabs take the
 * labels, and each section's own nodes move into its panel, so Shiki's code and the copy buttons are kept. Without
 * this script the sections read one after another, labelled.
 */
export function DocTabs() {
  useEffect(() => {
    for (const block of document.querySelectorAll<HTMLElement>('article [data-doc-tabs]')) {
      const sections = Array.from(block.children) as HTMLElement[]
      const mount = document.createElement('div')
      block.replaceWith(mount)
      render(
        <Tabs className="my-4" defaultValue="0">
          <TabsList>
            {sections.map((s, i) => (
              <TabsTrigger key={i} value={`${i}`}>{s.dataset.tab}</TabsTrigger>
            ))}
          </TabsList>
          {sections.map((_, i) => (
            // The panel has no children, so Preact never touches the nodes moved into it below.
            <TabsContent key={i} value={`${i}`} />
          ))}
        </Tabs>,
        mount,
      )
      mount.querySelectorAll<HTMLElement>('[role=tabpanel]').forEach((panel, i) => {
        sections[i].querySelector(':scope > [data-tab-label]')?.remove()
        panel.append(...sections[i].childNodes)
      })
    }
  }, [])
  return null
}
