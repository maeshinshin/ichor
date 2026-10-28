/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/stack"
	"github.com/maeshinshin/ichor/pkg/stack/argocd"
	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
	"github.com/maeshinshin/ichor/pkg/values"
)

// The stack controller (ADR 0099 decision 5, steps 4-9) lives in the IchorInstallation
// reconciler: one implementation for `ichor init` and for later upgrades.

// conditionStackReady is True when both components are published, have a Ready Order and a
// Synced/Healthy Application. The ADR names no stack condition of its own.
const conditionStackReady = "StackReady"

const (
	stackFieldManager = "ichor-bootstrap"
	stackOrderManager = "ichor-stack"

	// Secrets in the controller's namespace holding what is generated once and reused.
	kokumiAdminSecret = "ichor-kokumi-admin"
	argoRedisSecret   = "ichor-argocd-redis"

	secretKeyPassword = "password"
	secretKeyHash     = "password-hash"

	componentKokumi = "kokumi"
	componentArgoCD = "argocd"
	versionV1alpha1 = "v1alpha1"
	reasonReady     = "Ready"
	managedTrue     = "true"
	metaKey         = "metadata"
	nsKey           = "namespace"

	managedLabel      = "ichor.maesh.dev/managed"
	republishAnnotKey = "ichor.maesh.dev/republished"

	imageSource = "https://github.com/maeshinshin/ichor"

	stackPollInterval   = 10 * time.Second
	stackSteadyInterval = 5 * time.Minute
	registryIOTimeout   = 30 * time.Second
)

var (
	orderGVK       = schema.GroupVersionKind{Group: "delivery.kokumi.dev", Version: versionV1alpha1, Kind: "Order"}
	applicationGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: versionV1alpha1, Kind: "Application"}
)

// The controller applies nothing itself: the ichor-stack Job does (stack_job.go). It reads and
// writes only Jobs (the marker in ichor_upgrade.go), Orders, Applications and its own Secrets.
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;create;patch

// stackComponent is one of the two bootstrap components the controller publishes.
type stackComponent struct {
	name      string // also the Order, Application and repository name
	namespace string
	version   string // chart or release version, the tag prefix
	// manifest is the bundle that gets published: the rendered manifest without its Secrets.
	manifest []byte
	status   *ichorv1alpha1.IchorStackComponentStatus
	// valuesJSON is the user layer handed to the Job (Argo CD only).
	valuesJSON string
	// republished is set when this pass had to push the bundle again.
	republished bool
}

// stackWait reports why the bootstrap cannot go on yet.
type stackWait struct{ reason, message string }

func (w *stackWait) Error() string { return w.reason + ": " + w.message }

// reconcileStack runs the ordered, idempotent bootstrap and steady state, and writes
// status.stack and the StackReady condition into inst (the caller persists them). It
// returns when to look again.
func (r *IchorInstallationReconciler) reconcileStack(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation,
) (time.Duration, error) {
	after, err := r.runStack(ctx, inst)

	cond := metav1.Condition{
		Type: conditionStackReady, Status: metav1.ConditionTrue, Reason: reasonReady,
		Message: "Kokumi and Argo CD are published, ordered and Synced/Healthy", ObservedGeneration: inst.Generation,
	}

	var wait *stackWait

	switch {
	case errors.As(err, &wait):
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, wait.reason, wait.message
		after, err = stackPollInterval, nil
	case err != nil:
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "Failed", err.Error()
	}

	meta.SetStatusCondition(&inst.Status.Conditions, cond)

	return after, err
}

func (r *IchorInstallationReconciler) runStack(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation,
) (time.Duration, error) {
	if inst.Status.Stack == nil {
		inst.Status.Stack = &ichorv1alpha1.IchorStackStatus{}
	}

	st := inst.Status.Stack
	if st.Kokumi == nil {
		st.Kokumi = &ichorv1alpha1.IchorStackComponentStatus{}
	}

	if st.ArgoCD == nil {
		st.ArgoCD = &ichorv1alpha1.IchorStackComponentStatus{}
	}

	comps, err := r.stackComponents(ctx, inst)
	if err != nil {
		return 0, err
	}

	kok, arg := comps[0], comps[1]

	// Step 4: the ichor-stack Job applies Kokumi once and waits for its registry.
	if err := r.bootstrap(ctx, kok, inst); err != nil {
		return 0, err
	}

	// Step 5: the same for Argo CD. Its Secrets, the repo-creds one included, are created by
	// the Job directly, never published (step 6 of the ADR, as amended).
	if err := r.bootstrap(ctx, arg, inst); err != nil {
		return 0, err
	}

	// Step 7a: publish both bundles into Kokumi's registry.
	for _, c := range comps {
		if err := r.publish(ctx, c); err != nil {
			return 0, err
		}
	}

	// Step 7b: one Order per component.
	promotion := ichorv1alpha1.IchorStackPromotionAutomatic
	if inst.Spec.Stack != nil && inst.Spec.Stack.Promotion != "" {
		promotion = inst.Spec.Stack.Promotion
	}

	for _, c := range comps {
		if err := r.ensureOrder(ctx, c, promotion); err != nil {
			return 0, err
		}
	}

	// Step 8: each Job applies its component's CiliumNetworkPolicies after the workloads are
	// Ready (stackapply.Apply); the published bundles carry them too, so Argo CD keeps them.

	// Step 9: report.
	healthy := true

	for _, c := range comps {
		ready, err := r.orderReady(ctx, c)
		if err != nil {
			return 0, err
		}

		c.status.OrderReady = ready

		health, err := r.applicationHealth(ctx, c)
		if err != nil {
			return 0, err
		}

		c.status.ApplicationHealth = health
		healthy = healthy && ready && health == healthySynced
	}

	if !healthy {
		return 0, &stackWait{"WaitingForDelivery", "waiting for Kokumi's Orders to be Ready and Argo CD's Applications to be Synced/Healthy"}
	}

	return stackSteadyInterval, nil
}

const healthySynced = "Synced/Healthy"

// stackComponents renders both bundles from the stored passwords and the user's values.
func (r *IchorInstallationReconciler) stackComponents(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation,
) ([]*stackComponent, error) {
	hash, redisPassword, err := r.stackSecrets(ctx)
	if err != nil {
		return nil, err
	}

	userValues := map[string]map[string]any{}

	if s := inst.Spec.Stack; s != nil {
		for n, c := range map[string]*ichorv1alpha1.IchorStackComponentSpec{componentKokumi: s.Kokumi, componentArgoCD: s.ArgoCD} {
			if c == nil || c.Values == nil || len(c.Values.Raw) == 0 {
				continue
			}

			m := map[string]any{}
			if err := json.Unmarshal(c.Values.Raw, &m); err != nil {
				return nil, fmt.Errorf("spec.stack.%s.values: %w", n, err)
			}

			if err := values.CheckSize(m); err != nil {
				return nil, fmt.Errorf("spec.stack.%s.values: %w", n, err)
			}

			userValues[n] = m
		}
	}

	// ponytail: Kokumi has no chart; no key is mapped to a patch yet, so any value is refused.
	if len(userValues[componentKokumi]) > 0 {
		return nil, errors.New("spec.stack.kokumi.values: no key is supported yet")
	}

	kb, err := kokumi.Render(kokumi.Options{AdminPasswordHash: hash})
	if err != nil {
		return nil, err
	}

	ab, err := argocd.Render(argocd.Options{Values: userValues[componentArgoCD], RedisPassword: redisPassword})
	if err != nil {
		return nil, err
	}

	// Kokumi's registry is anonymous: no Secret may be in what is published.
	if kb, _, err = stack.SplitSecrets(kb); err != nil {
		return nil, err
	}

	if ab, _, err = stack.SplitSecrets(ab); err != nil {
		return nil, err
	}

	argoJSON := ""
	if len(userValues[componentArgoCD]) > 0 {
		raw, err := json.Marshal(userValues[componentArgoCD])
		if err != nil {
			return nil, err
		}

		argoJSON = string(raw)
	}

	st := inst.Status.Stack

	return []*stackComponent{
		{
			name: componentKokumi, namespace: kokumi.Namespace, version: kokumi.Version, manifest: kb, status: st.Kokumi,
		},
		{
			name: componentArgoCD, namespace: argocd.Namespace, version: argocd.ChartVersion, manifest: ab, status: st.ArgoCD,
			valuesJSON: argoJSON,
		},
	}, nil
}

// stackSecrets returns the Kokumi admin password hash and the Argo CD redis password, creating
// each Secret on first use and reusing it afterwards, so Render stays deterministic. Neither
// value is logged or reported.
func (r *IchorInstallationReconciler) stackSecrets(ctx context.Context) (hash, redisPassword string, err error) {
	admin, err := r.getOrCreateSecret(ctx, kokumiAdminSecret, func() (map[string][]byte, error) {
		pw, h, err := kokumi.NewAdminPassword()
		if err != nil {
			return nil, err
		}

		return map[string][]byte{secretKeyPassword: []byte(pw), secretKeyHash: []byte(h)}, nil
	})
	if err != nil {
		return "", "", err
	}

	redis, err := r.getOrCreateSecret(ctx, argoRedisSecret, func() (map[string][]byte, error) {
		return map[string][]byte{secretKeyPassword: []byte(rand.Text() + rand.Text())}, nil
	})
	if err != nil {
		return "", "", err
	}

	if len(admin[secretKeyHash]) == 0 || len(redis[secretKeyPassword]) == 0 {
		return "", "", fmt.Errorf("secrets %s and %s are missing their keys", kokumiAdminSecret, argoRedisSecret)
	}

	return string(admin[secretKeyHash]), string(redis[secretKeyPassword]), nil
}

func (r *IchorInstallationReconciler) getOrCreateSecret(
	ctx context.Context, secretName string, gen func() (map[string][]byte, error),
) (map[string][]byte, error) {
	key := types.NamespacedName{Namespace: r.ControllerNamespace, Name: secretName}

	var s corev1.Secret

	err := r.Direct.Get(ctx, key, &s)
	if err == nil {
		return s.Data, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("reading Secret %s: %w", secretName, err)
	}

	data, err := gen()
	if err != nil {
		return nil, err
	}

	s = corev1.Secret{
		Namespace: key.Namespace, Name: key.Name, Labels: map[string]string{managedLabel: managedTrue},
		Data: data,
	}
	if err := r.Direct.Create(ctx, &s); err != nil {
		return nil, fmt.Errorf("creating Secret %s: %w", secretName, err)
	}

	return data, nil
}

// bootstrap has the component applied once, by its Job, unless its Order already exists (from
// then on Argo CD owns the objects, decision 11). Until the Job has completed it is a wait.
func (r *IchorInstallationReconciler) bootstrap(
	ctx context.Context, c *stackComponent, inst *ichorv1alpha1.IchorInstallation,
) error {
	exists, err := r.orderExists(ctx, c)
	if err != nil || exists {
		return err
	}

	done, err := r.ensureStackJob(ctx, c, inst)
	if err != nil {
		return err
	}

	if !done {
		return &stackWait{"WaitingFor" + titleName(c.name), "Job " + stackJobName(c.name) + " is applying " + c.name}
	}

	return nil
}

func titleName(n string) string {
	if n == componentArgoCD {
		return "ArgoCD"
	}

	return "Kokumi"
}

func (r *IchorInstallationReconciler) orderExists(ctx context.Context, c *stackComponent) (bool, error) {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(orderGVK)

	err := r.Direct.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: c.name}, o)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, nil
	}

	return err == nil, err
}

func (r *IchorInstallationReconciler) registryHost() string {
	if r.RegistryHost != "" {
		return r.RegistryHost
	}

	return kokumi.RegistryHost
}

// publish pushes the component's bundle unless the registry already has the tag, and records
// the tag and digest. A tag that vanished (registry rebuilt) is pushed again.
func (r *IchorInstallationReconciler) publish(ctx context.Context, c *stackComponent) error {
	ctx, cancel := context.WithTimeout(ctx, registryIOTimeout)
	defer cancel()

	tag := stack.Tag(c.version, c.manifest)
	repo := r.registryHost() + "/ichor/" + c.name

	ref, err := name.NewTag(repo+":"+tag, name.Insecure)
	if err != nil {
		return fmt.Errorf("reference %s:%s: %w", repo, tag, err)
	}

	if d, err := remote.Head(ref, remote.WithContext(ctx)); err == nil {
		c.status.PublishedTag, c.status.Digest = tag, d.Digest.String()

		return nil
	}

	digest, err := stack.Push(ctx, repo, tag, c.manifest, map[string]string{
		"org.opencontainers.image.source":  imageSource,
		"org.opencontainers.image.version": c.version,
	})
	if err != nil {
		return &stackWait{"WaitingForRegistry", "publishing " + c.name + " failed: " + err.Error()}
	}

	c.status.PublishedTag, c.status.Digest, c.republished = tag, digest, true

	return nil
}

// ensureOrder creates or updates the component's Order so it names the published tag.
func (r *IchorInstallationReconciler) ensureOrder(ctx context.Context, c *stackComponent, promotion string) error {
	source := "oci://" + kokumi.RegistryHost + "/ichor/" + c.name

	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(orderGVK)

	err := r.Direct.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: c.name}, o)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("reading Order %s/%s: %w", c.namespace, c.name, err)
	}

	if err == nil && !c.republished && orderMatches(o, source, c.status.PublishedTag, promotion) {
		return nil
	}

	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": orderGVK.GroupVersion().String(), "kind": orderGVK.Kind,
		metaKey: map[string]any{
			labelName: c.name, nsKey: c.namespace,
			"labels": map[string]any{managedLabel: managedTrue},
		},
		"spec": map[string]any{
			"source":    map[string]any{"oci": source, "version": c.status.PublishedTag},
			"promotion": map[string]any{"mode": promotion},
		},
	}}

	if c.republished {
		// ponytail: whether Kokumi re-renders a Preparation whose artifact vanished is an
		// open question (ADR 0099); touching an annotation is the cheapest nudge.
		desired.SetAnnotations(map[string]string{republishAnnotKey: time.Now().UTC().Format(time.RFC3339)})
	}

	if err := r.Direct.Apply(ctx, client.ApplyConfigurationFromUnstructured(desired),
		client.FieldOwner(stackOrderManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying Order %s/%s: %w", c.namespace, c.name, err)
	}

	return nil
}

func orderMatches(o *unstructured.Unstructured, source, tag, promotion string) bool {
	gotSource, _, _ := unstructured.NestedString(o.Object, "spec", "source", "oci")
	gotTag, _, _ := unstructured.NestedString(o.Object, "spec", "source", "version")
	gotMode, _, _ := unstructured.NestedString(o.Object, "spec", "promotion", "mode")

	return gotSource == source && gotTag == tag && gotMode == promotion && o.GetLabels()[managedLabel] == managedTrue
}

// orderReady reads the Order's Ready condition.
func (r *IchorInstallationReconciler) orderReady(ctx context.Context, c *stackComponent) (bool, error) {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(orderGVK)

	if err := r.Direct.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: c.name}, o); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok && m["type"] == "Ready" {
			return m["status"] == string(metav1.ConditionTrue), nil
		}
	}

	return false, nil
}

// applicationHealth is "<sync>/<health>" of the Application Kokumi's Serving created for the
// Order (same name, in the argocd namespace), or "" while there is none.
func (r *IchorInstallationReconciler) applicationHealth(ctx context.Context, c *stackComponent) (string, error) {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)

	err := r.Direct.Get(ctx, types.NamespacedName{Namespace: argocd.Namespace, Name: c.name}, app)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("reading Application %s: %w", c.name, err)
	}

	sync, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	health, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")

	if sync == "" && health == "" {
		return "", nil
	}

	return sync + "/" + health, nil
}
