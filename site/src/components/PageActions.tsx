import { ChevronDown, Copy, FileText, Link, Share2 } from 'lucide-preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ButtonGroup } from '@/components/ui/button-group'
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Toaster } from '@/components/ui/sonner'
import { toast } from '@/lib/shims/sonner'

type Props = { title: string; /** Canonical URL of this page. */ url: string; /** Site path of this page's Markdown. */ mdUrl: string }

/** Puts text on the clipboard. The text may still be loading: Safari only allows that through a ClipboardItem. */
async function copy(text: Promise<string>, done: string) {
  try {
    if (typeof ClipboardItem === 'undefined') await navigator.clipboard.writeText(await text)
    else await navigator.clipboard.write([new ClipboardItem({ 'text/plain': text.then((t) => new Blob([t], { type: 'text/plain' })) })])
    toast.success(done)
  } catch {
    toast.error('Could not copy')
  }
}

const markdown = (mdUrl: string) =>
  fetch(mdUrl).then((r) => {
    if (!r.ok) throw new Error(r.statusText)
    return r.text()
  })

/**
 * "Copy page" for the article, and the copy-link button of every h2/h3 (the anchors are
 * rendered into the page by Prose.astro; this island only copies their URL on click, the
 * browser already moves the address bar). One island, so there is one Toaster.
 */
export function PageActions({ title, url, mdUrl }: Props) {
  const [canShare, setCanShare] = useState(false)
  const [dark, setDark] = useState(false)
  useEffect(() => {
    setCanShare(typeof navigator.share === 'function')
    const root = document.documentElement
    const sync = () => setDark(root.classList.contains('dark'))
    sync()
    const watch = new MutationObserver(sync)
    watch.observe(root, { attributes: true, attributeFilter: ['class'] })
    const onClick = (e: MouseEvent) => {
      const a = (e.target as Element).closest<HTMLAnchorElement>('a[data-heading-link]')
      if (a) copy(Promise.resolve(a.href), 'Link to section copied')
    }
    document.addEventListener('click', onClick)
    return () => {
      watch.disconnect()
      document.removeEventListener('click', onClick)
    }
  }, [])

  const copyMarkdown = () => copy(markdown(mdUrl), 'Page copied as Markdown')
  return (
    <>
      <ButtonGroup>
        <Button variant="outline" size="sm" onClick={copyMarkdown}>
          <Copy />
          Copy page
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon-sm" aria-label="More page actions">
              <ChevronDown className="transition-transform group-data-[state=open]/button:rotate-180" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={copyMarkdown}>
                <Copy />
                Copy as Markdown
              </DropdownMenuItem>
              <DropdownMenuItem asChild>
                <a href={mdUrl} target="_blank" rel="noreferrer">
                  <FileText />
                  View as Markdown
                </a>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => copy(Promise.resolve(url), 'Link copied')}>
                <Link />
                Copy link
              </DropdownMenuItem>
              {canShare && (
                <DropdownMenuItem onSelect={() => navigator.share({ title, url }).catch(() => {})}>
                  <Share2 />
                  Share…
                </DropdownMenuItem>
              )}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </ButtonGroup>
      <Toaster theme={dark ? 'dark' : 'light'} />
    </>
  )
}
