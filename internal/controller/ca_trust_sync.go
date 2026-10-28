package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// caTrustSyncInterval is how often CATrustSync looks; it must be well below the time between a
// rotation's start and its Issuing stage (several node applies), which is what the early publish needs.
const caTrustSyncInterval = 10 * time.Second

// CATrustSync keeps this process's kubetrust pool right on every replica, leader or not.
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
}

// NeedLeaderElection is false: every replica needs its own pool.
func (s *CATrustSync) NeedLeaderElection() bool { return false }

// Start runs until ctx ends.
func (s *CATrustSync) Start(ctx context.Context) error {
	interval := s.Interval
	if interval == 0 {
		interval = caTrustSyncInterval
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		s.syncOnce(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
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
		if err != nil || rot == nil {
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
