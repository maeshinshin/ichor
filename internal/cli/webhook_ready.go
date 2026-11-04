package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The controller Deployment and the Service of its admission webhooks, as Ichor's manifest and
// chart name them. ponytail: a Helm release with another name or namespace is not looked up by
// label; the wait then times out with the message below.
const (
	controllerDeploymentName = "ichor-controller-manager"
	webhookServiceName       = "ichor-webhook-service"
)

// waitForController is waitForWebhookServer; a var so the tests that run init and adopt against
// a fake cluster, which has no controller, can switch the wait off.
var waitForController = waitForWebhookServer

// webhookServerNotReady says why the controller cannot serve Ichor's webhooks yet, or returns
// nil when it can: its Deployment is rolled out with an available replica, and the webhook
// Service has a ready endpoint (what the API server needs to call the webhook).
func webhookServerNotReady(ctx context.Context, c client.Reader) error {
	var d appsv1.Deployment

	switch err := c.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: controllerDeploymentName}, &d); {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("the Deployment %s does not exist yet", controllerDeploymentName)
	case err != nil:
		return err
	}

	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}

	st := d.Status
	if st.ObservedGeneration < d.Generation || st.UpdatedReplicas < want || st.Replicas != st.UpdatedReplicas || st.AvailableReplicas < 1 {
		return fmt.Errorf("the Deployment %s is not rolled out (%d updated, %d available of %d)",
			controllerDeploymentName, st.UpdatedReplicas, st.AvailableReplicas, want)
	}

	var slices discoveryv1.EndpointSliceList

	err := c.List(ctx, &slices, client.InNamespace(ichorControllerNamespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: webhookServiceName})
	// ponytail: an operator who may not read EndpointSlices relies on the Deployment alone.
	if apierrors.IsForbidden(err) {
		return nil
	}

	if err != nil {
		return err
	}

	for i := range slices.Items {
		for _, e := range slices.Items[i].Endpoints {
			if e.Conditions.Ready == nil || *e.Conditions.Ready { // nil means ready
				return nil
			}
		}
	}

	return fmt.Errorf("the Service %s has no ready endpoint", webhookServiceName)
}

// waitForWebhookServer waits, for up to timeout, until the controller serves Ichor's admission
// webhooks (webhookServerNotReady), so the first CR write is not the one that finds out. It
// prints what is missing at each poll and why the pods are not running, and it ends early on a
// cluster with no pod network.
func waitForWebhookServer(ctx context.Context, c client.Reader, timeout, interval time.Duration, progress io.Writer) error {
	d := newWaitDiag(c, progress)

	err := pollWhile(ctx, timeout, interval, progress, "the controller to serve Ichor's webhooks", func(ctx context.Context) error {
		err := webhookServerNotReady(ctx, c)
		if err == nil {
			return nil
		}

		if fatal := d.tick(ctx); fatal != nil {
			return fatal
		}

		return err
	}, func(err error) bool { return !errors.Is(err, errNoPodNetwork) })
	if err == nil || ctx.Err() != nil || errors.Is(err, errNoPodNetwork) {
		return err
	}

	msg := fmt.Sprintf("the controller Deployment %s in %s is not Ready after %s", controllerDeploymentName, ichorControllerNamespace, timeout)
	if s := d.summary(ctx); s != "" {
		msg += " (" + s + ")"
	}

	return fmt.Errorf("%s: %w; check `kubectl -n %s get pods`", msg, err, ichorControllerNamespace)
}
