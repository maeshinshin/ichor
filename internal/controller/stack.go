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
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/stack"
	"github.com/maeshinshin/ichor/pkg/stack/argocd"
	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
	"github.com/maeshinshin/ichor/pkg/values"
)

// The stack controller (steps 4-9) lives in the IchorInstallation
// reconciler: one implementation for `ichor init` and for later upgrades.

// conditionStackReady is True when both components are published, have a Ready Order and a
// Synced/Healthy Application.
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

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;create;patch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;create;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs=get;create;patch;escalate;bind
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicies;validatingadmissionpolicybindings,verbs=get;create;patch
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;services;serviceaccounts,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=create;patch
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=create;patch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get
// +kubebuilder:rbac:groups=delivery.kokumi.dev,resources=orders,verbs=get;create;patch

// stackComponent is one of the two bootstrap components the controller publishes.
type stackComponent struct {
	name      string // also the Order, Application and repository name
	namespace string
	version   string // chart or release version, the tag prefix
	manifest  []byte
	status    *ichorv1alpha1.IchorStackComponentStatus
	// waitFor lists the Deployments that must be Ready after the first apply.
	waitFor []string
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

	// Step 4: apply Kokumi once, wait for its registry.
	if err := r.bootstrap(ctx, kok, false); err != nil {
		return 0, err
	}

	// Step 5: apply Argo CD once (its manifest carries the repo-creds Secret and the default
	// AppProject, steps 6), wait for it.
	if err := r.bootstrap(ctx, arg, true); err != nil {
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

	// Step 8: the network policies of unit 13 go here, once both namespaces have workloads.
	if err := r.applyStackPolicies(ctx); err != nil {
		return 0, err
	}

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

// applyStackPolicies is the hook for the CiliumNetworkPolicies of the kokumi and argocd
// namespaces. ponytail: nothing to apply until unit 13.
func (r *IchorInstallationReconciler) applyStackPolicies(context.Context) error { return nil }

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

	st := inst.Status.Stack

	return []*stackComponent{
		{
			name: componentKokumi, namespace: kokumi.Namespace, version: kokumi.Version, manifest: kb, status: st.Kokumi,
			waitFor: []string{"kokumi-registry"},
		},
		{
			name: componentArgoCD, namespace: argocd.Namespace, version: argocd.ChartVersion, manifest: ab, status: st.ArgoCD,
			waitFor: []string{"argocd-server", "argocd-repo-server"},
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

// bootstrap server-side-applies the component's manifest unless its Order already exists (from
// then on Argo CD owns the objects, decision 11), then waits for its Deployments.
func (r *IchorInstallationReconciler) bootstrap(ctx context.Context, c *stackComponent, ensureNamespace bool) error {
	exists, err := r.orderExists(ctx, c)
	if err != nil {
		return err
	}

	if !exists {
		if ensureNamespace {
			if err := r.Direct.Apply(ctx, client.ApplyConfigurationFromUnstructured(namespaceObject(c.namespace)),
				client.FieldOwner(stackFieldManager), client.ForceOwnership); err != nil {
				return fmt.Errorf("applying Namespace %s: %w", c.namespace, err)
			}
		}

		if err := r.applyManifest(ctx, c); err != nil {
			return err
		}
	}

	for _, d := range c.waitFor {
		var dep appsv1.Deployment

		err := r.Direct.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: d}, &dep)
		if apierrors.IsNotFound(err) {
			return &stackWait{"WaitingFor" + titleName(c.name), "Deployment " + c.namespace + "/" + d + " does not exist yet"}
		} else if err != nil {
			return fmt.Errorf("reading Deployment %s/%s: %w", c.namespace, d, err)
		}

		want := int32(1)
		if dep.Spec.Replicas != nil {
			want = *dep.Spec.Replicas
		}

		if dep.Status.ReadyReplicas < want {
			return &stackWait{"WaitingFor" + titleName(c.name), "Deployment " + c.namespace + "/" + d + " is not Ready"}
		}
	}

	return nil
}

func titleName(n string) string {
	if n == componentArgoCD {
		return "ArgoCD"
	}

	return "Kokumi"
}

func namespaceObject(n string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", metaKey: map[string]any{labelName: n},
	}}
}

// applyManifest applies the multi-document manifest in order. A kind whose CRD was applied a
// moment ago may not be served yet; that is a wait, not a failure.
func (r *IchorInstallationReconciler) applyManifest(ctx context.Context, c *stackComponent) error {
	rd := yamlutil.NewYAMLReader(bufio.NewReader(bytes.NewReader(c.manifest)))

	for {
		doc, err := rd.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("splitting the %s manifest: %w", c.name, err)
		}

		obj := map[string]any{}
		if err := sigsyaml.Unmarshal(doc, &obj); err != nil {
			return fmt.Errorf("parsing the %s manifest: %w", c.name, err)
		}

		if len(obj) == 0 {
			continue
		}

		u := &unstructured.Unstructured{Object: obj}

		if u.GetNamespace() == "" {
			mapping, err := r.Direct.RESTMapper().RESTMapping(u.GroupVersionKind().GroupKind(), u.GroupVersionKind().Version)
			if meta.IsNoMatchError(err) {
				return &stackWait{"WaitingFor" + titleName(c.name), "waiting for " + u.GetKind() + " to be served"}
			} else if err != nil {
				return fmt.Errorf("mapping %s: %w", u.GetKind(), err)
			}

			if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
				u.SetNamespace(c.namespace)
			}
		}

		err = r.Direct.Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
			client.FieldOwner(stackFieldManager), client.ForceOwnership)
		if meta.IsNoMatchError(err) {
			return &stackWait{"WaitingFor" + titleName(c.name), "waiting for " + u.GetKind() + " to be served"}
		} else if err != nil {
			return fmt.Errorf("applying %s %s: %w", u.GetKind(), u.GetName(), err)
		}
	}
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
		// open question; touching an annotation is the cheapest nudge.
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
