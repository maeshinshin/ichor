import { OperationKind as OperationKindValues, type OperationKind } from '@/gen/ichor/v1alpha1/operation_pb'
import type { OpKind } from '@/lib/operations'

// The screens' kind word as the wire enum.
export function wireKindOf(k: OpKind): OperationKind {
  switch (k) {
    case 'talos':
      return OperationKindValues.TALOS
    case 'kubernetes':
      return OperationKindValues.KUBERNETES
    case 'ichor':
      return OperationKindValues.ICHOR
    case 'cni':
      return OperationKindValues.CNI
    case 'shutdown':
      return OperationKindValues.SHUTDOWN
    case 'ca-rotation':
      return OperationKindValues.CA_ROTATION
    case 'config':
      return OperationKindValues.CONFIG
    case 'key-rotation':
      return OperationKindValues.KEY_ROTATION
  }
}
