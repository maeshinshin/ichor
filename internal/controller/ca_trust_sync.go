package controller

import (
	"cmp"
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// caTrustSyncInterval is the fallback tick of CATrustSync (and how often it clears). It is NOT fast
// enough for the early publish: Accepting to Issuing took 6-8 s on hardware, so the publish is driven by
// operation events (watchOperations) and this tick only backs it up.
const caTrustSyncInterval = 10 * time.Second

// CATrustSync keeps this process's kubetrust pool right on every replica, leader or not (ADR 0063).
// The rotation runs on the leader only, but the webhooks and informers of a follower use the same
// kind of API client and would fail x509 against the new serving certificate until their projected
// ca.crt refreshes. While a CA rotation is under way it publishes the new Kubernetes CA's certificate
// (never its key); with none under way it drops the extras once the projected ca.crt carries the newest
// of them, so a superseded CA is not trusted until the pod is recreated. A restarted container (the
// trust file survives it) clears on its first pass.
type CATrustSync struct {
	Reader              client.Reader
	ControllerNamespace string
	Pool                *kubetrust.Pool
	File                string
	Interval            time.Duration
	// Informers, when set, is the manager's cache: a CA rotation that appears or changes publishes at
	// once instead of at the next tick. The informers run on every replica, leader or not.
	Informers operationInformers
}

// operationInformers is the part of the manager's cache CATrustSync needs.
type operationInformers interface {
	GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error)
}

// NeedLeaderElection is false: every replica needs its own pool.
func (s *CATrustSync) NeedLeaderElection() bool { return false }

// Start runs until ctx ends. Passes run only in this loop, so the tick and the event trigger never race.
func (s *CATrustSync) Start(ctx context.Context) error {
	t := time.NewTicker(cmp.Or(s.Interval, caTrustSyncInterval))
	defer t.Stop()

	trigger := make(chan struct{}, 1)
	s.watchOperations(ctx, trigger)

	for {
		s.syncOnce(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-trigger:
		}
	}
}

// watchOperations asks for a pass whenever a CA rotation that is under way is added or changes. The
// tick stays as the fallback (and does the clearing), so a failure here only costs promptness.
func (s *CATrustSync) watchOperations(ctx context.Context, trigger chan<- struct{}) {
	if s.Informers == nil {
		return
	}

	wake := func(obj any) {
		if op, ok := obj.(*ichorv1alpha1.TalosOperation); ok && caRotationUnderway(op) {
			select {
			case trigger <- struct{}{}:
			default: // a pass is already requested
			}
		}
	}

	inf, err := s.Informers.GetInformer(ctx, &ichorv1alpha1.TalosOperation{})
	if err == nil {
		_, err = inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc:    wake,
			UpdateFunc: func(_, obj any) { wake(obj) },
		})
	}

	if err != nil {
		ctrl.LoggerFrom(ctx).WithName("catrustsync").Error(err, "Could not watch operations; relying on the tick")
	}
}

// syncOnce does one pass; errors are logged and retried by the next tick (an unreachable API server
// during a rotation is expected).
func (s *CATrustSync) syncOnce(ctx context.Context) {
	log := ctrl.LoggerFrom(ctx).WithName("catrustsync")

	var ops ichorv1alpha1.TalosOperationList
	if err := s.Reader.List(ctx, &ops); err != nil {
		log.V(1).Info("Could not list operations", "error", err.Error())

		return
	}

	underway := false

	for i := range ops.Items {
		op := &ops.Items[i]
		if !caRotationUnderway(op) {
			continue
		}

		underway = true

		var cluster ichorv1alpha1.TalosCluster
		if err := s.Reader.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster); err != nil {
			log.V(1).Info("Could not read the rotation's cluster", "error", err.Error())

			continue
		}

		rot, err := secretsbundle.LoadRotation(ctx, s.Reader, s.ControllerNamespace, cluster.Spec.SecretsRef.Name)
		// An etcd or aggregator rotation alone brings no new Kubernetes CA to trust (ADR 0120).
		if err != nil || rot == nil || rot.Kubernetes == nil {
			continue
		}

		if err := s.Pool.Publish(rot.Kubernetes.Crt, s.File); err != nil {
			log.Error(err, "Could not publish the new Kubernetes CA to this replica's trust pool")
		}
	}

	if underway {
		return
	}

	if _, err := s.Pool.ClearOnceBaseTrusts(s.File); err != nil {
		log.Error(err, "Could not clear the extra trusted CAs")
	}
}
