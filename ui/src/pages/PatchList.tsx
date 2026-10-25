import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfigLayer } from '@/gen/ichor/v1alpha1/node_pb'
import type { Patch } from '@/gen/ichor/v1alpha1/patch_pb'
import { clusterClient, patchClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { targetKindOf } from '@/lib/patchTarget'
import { Link, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

// Patch list: every TalosConfigPatch the backend can see, across clusters
// (PatchService.ListPatches with no cluster filter, same "list everything,
// unconditionally" convention MachineList already uses for pending
// machines). Create/edit/delete all live here and on PatchDetail
// (architecture.md, "CLI and UI": "config editing by layer").
export function PatchList() {
  const [patches, setPatches] = useState<Patch[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [clusterNames, setClusterNames] = useState<Record<string, string>>({})

  async function reload() {
    try {
      const [patchesRes, clustersRes] = await Promise.all([
        patchClient.listPatches({}),
        clusterClient.listClusters({}),
      ])
      setPatches(patchesRes.patches)
      setClusterNames(
        Object.fromEntries(
          clustersRes.clusters
            .filter((c) => c.ref)
            .map((c) => [`${c.ref!.namespace}/${c.ref!.name}`, c.ref!.name]),
        ),
      )
      setError(null)
    } catch (err) {
      setError(describeError(err))
    }
  }

  useEffect(() => {
    reload()
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!patches) return <p class="mt-10 text-center text-sm text-muted-foreground">Loading patches…</p>

  return (
    <Card className="mt-6">
      <CardHeader className="flex items-center justify-between">
        <CardTitle>Config patches</CardTitle>
        <Button asChild size="sm">
          <Link to="/patches/new">New patch</Link>
        </Button>
      </CardHeader>
      <CardContent>
        {patches.length === 0 ? (
          <p class="text-sm text-muted-foreground">No patches yet.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Cluster</TableHead>
                <TableHead>Layer</TableHead>
                <TableHead>Priority</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Documents</TableHead>
                <TableHead>Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {patches.map((p) => (
                <TableRow key={`${p.ref?.namespace}/${p.ref?.name}`}>
                  <TableCell>
                    {p.ref ? (
                      <Link to={objectRefPath('/patches', p.ref)} class="font-medium hover:underline">
                        {p.ref.name}
                      </Link>
                    ) : (
                      '-'
                    )}
                  </TableCell>
                  <TableCell>
                    {p.cluster ? clusterNames[`${p.cluster.namespace}/${p.cluster.name}`] ?? p.cluster.name : '-'}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{enumLabel(ConfigLayer, p.layer)}</Badge>
                  </TableCell>
                  <TableCell>{p.priority}</TableCell>
                  <TableCell>{targetSummary(p)}</TableCell>
                  <TableCell>{p.documents.length === 0 ? '-' : p.documents.map((d) => d.kind).join(', ')}</TableCell>
                  <TableCell>
                    <div class="flex gap-2">
                      <Button asChild size="sm" variant="outline">
                        <Link to={objectRefPath('/patches', p.ref ?? { namespace: '', name: '' })}>Edit</Link>
                      </Button>
                      <DeleteDialog patch={p} onDeleted={reload} />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function targetSummary(p: Patch): string {
  if (p.layer === ConfigLayer.CLUSTER) return '-'
  switch (targetKindOf(p.target)) {
    case 'role':
      return `Role: ${p.target!.role}`
    case 'labelSelector':
      return 'Label selector'
    case 'machineRef':
      return `Machine: ${p.target!.machineRef}`
    default:
      return '-'
  }
}

function DeleteDialog({ patch, onDeleted }: { patch: Patch; onDeleted: () => void }) {
  const [open, setOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  async function submit() {
    if (!patch.ref) return
    setSubmitting(true)
    setError(null)
    try {
      await patchClient.deletePatch({ patch: patch.ref })
      setOpen(false)
      onDeleted()
    } catch (err) {
      setError(describeError(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="destructive">
          Delete
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {patch.ref?.name}?</DialogTitle>
          <DialogDescription>
            This removes the TalosConfigPatch outright, unlike rejecting a machine - there is no durable record
            left behind and nothing will recreate it. Nodes it targets stop receiving its values the next time
            the controller renders their configuration.
          </DialogDescription>
        </DialogHeader>
        {error && <ErrorAlert {...error} />}
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={submitting} onClick={submit}>
            {submitting ? 'Deleting…' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
