import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Placeholder } from '@/pages/Placeholder'
import { Link, usePath } from '@/router'

// The route table. Every entry is a stub page until its phase lands; this
// is the "place pages go in" the phase-5 skeleton asks for.
const routes: { path: string; label: string; title: string }[] = [
  { path: '/', label: 'Clusters', title: 'Clusters' },
  { path: '/gitops', label: 'GitOps', title: 'GitOps' },
]

export function App() {
  const path = usePath()
  const route = routes.find((r) => r.path === path)

  return (
    <div class="mx-auto min-h-screen max-w-3xl px-4">
      <header class="flex items-center gap-2 py-4">
        <span class="text-base font-semibold">Ichor</span>
        <nav class="ml-6 flex gap-1">
          {routes.map((r) => (
            <Button key={r.path} asChild variant={path === r.path ? 'secondary' : 'ghost'} size="sm">
              <Link to={r.path}>{r.label}</Link>
            </Button>
          ))}
        </nav>
      </header>
      <Separator />
      <main>
        <Placeholder title={route ? route.title : 'Not found'} />
      </main>
    </div>
  )
}
