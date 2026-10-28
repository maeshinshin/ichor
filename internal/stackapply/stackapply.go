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

// Package stackapply is what the one-shot ichor-stack Job runs: the
// first, and only, server-side apply of Kokumi or Argo CD, plus the Secrets that must never be
// part of a published bundle. It exists so the controller does not need the rights to create
// RBAC, CRDs and workloads; the Job's own ServiceAccount has them (config/rbac/ichor-stack).
package stackapply

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/stack"
	"github.com/maeshinshin/ichor/pkg/stack/argocd"
	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
)

const (
	// FieldManager owns the applied objects until Argo CD takes them over.
	FieldManager = "ichor-bootstrap"

	// ComponentKokumi and ComponentArgoCD are the two components, in apply order.
	ComponentKokumi = "kokumi"
	ComponentArgoCD = "argocd"

	// ServiceAccount and JobLabel are shared with the controller, which builds the Job, and
	// with config/rbac/ichor-stack and the Cilium policy that selects the pod.
	ServiceAccount = "ichor-stack"
	JobLabel       = "ichor-stack"

	// EnvKokumiAdminHash and EnvArgoRedisPassword carry the two generated values into the
	// Job from the controller's Secrets (secretKeyRef, so the Job needs no rights on Secrets
	// in the controller's namespace).
	EnvKokumiAdminHash = "ICHOR_KOKUMI_ADMIN_HASH"
	EnvArgoRedisPasswd = "ICHOR_ARGOCD_REDIS_PASSWORD"

	annotationPrune     = "argocd.argoproj.io/sync-options"
	annotationCompare   = "argocd.argoproj.io/compare-options"
	managedByAnnotation = "ichor.maesh.dev/created-by"

	retryInterval = 2 * time.Second
	metaKey       = "metadata"
	kindNamespace = "Namespace"
)

// Options are the inputs to Apply.
type Options struct {
	// Component is ComponentKokumi or ComponentArgoCD.
	Component string
	// ArgoCDValues is the user layer for the Argo CD chart (JSON object); Kokumi takes none.
	ArgoCDValues map[string]any
	// KokumiAdminHash and ArgoRedisPassword are the generated values (see the Env constants).
	KokumiAdminHash, ArgoRedisPassword string
	// Timeout bounds the wait for CRDs to be served and for the Deployments to be Ready.
	Timeout time.Duration
	// Log receives one line per step; nil discards.
	Log io.Writer
}

// Render returns the component's full manifest (Secrets included), its namespace and the
// Deployments that must be Ready after the apply.
func Render(o Options) (manifest []byte, namespace string, waitFor []string, err error) {
	switch o.Component {
	case ComponentKokumi:
		m, err := kokumi.Render(kokumi.Options{AdminPasswordHash: o.KokumiAdminHash, OmitPolicies: true})

		return m, kokumi.Namespace, []string{"kokumi-registry"}, err
	case ComponentArgoCD:
		m, err := argocd.Render(argocd.Options{Values: o.ArgoCDValues, RedisPassword: o.ArgoRedisPassword, OmitPolicies: true})

		return m, argocd.Namespace, []string{"argocd-server", "argocd-repo-server"}, err
	}

	return nil, "", nil, fmt.Errorf("unknown component %q: want %s or %s", o.Component, ComponentKokumi, ComponentArgoCD)
}

// Apply server-side-applies the component (Namespace first, then its Secrets created once,
// then everything else), waits for its Deployments and returns. It is idempotent: a second
// run applies the same bytes and leaves existing Secrets alone.
func Apply(ctx context.Context, c client.Client, o Options) error {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}

	if o.Log == nil {
		o.Log = io.Discard
	}

	manifest, ns, waitFor, err := Render(o)
	if err != nil {
		return err
	}

	bundle, secrets, err := stack.SplitSecrets(manifest)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	// Argo CD's chart ships no Namespace; Kokumi's install.yaml does, and applying it again is a no-op.
	if o.Component == ComponentArgoCD {
		if err := applyOne(ctx, c, namespaceObject(ns)); err != nil {
			return err
		}
	}

	docs, err := parse(bundle) // everything but the Secrets, in order
	if err != nil {
		return err
	}

	// Namespaces, then Secrets, then the rest: the Secrets must exist before the pods that mount them.
	for _, u := range docs {
		if u.GetKind() == kindNamespace {
			if err := applyOne(ctx, c, u); err != nil {
				return err
			}
		}
	}

	for _, s := range secrets {
		if err := createSecret(ctx, c, s, ns); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(o.Log, "created Secret %s/%s if absent\n", s.GetNamespace(), s.GetName())
	}

	for _, u := range docs {
		if u.GetKind() == kindNamespace {
			continue
		}

		if u.GetNamespace() == "" {
			if err := defaultNamespace(ctx, c, u, ns); err != nil {
				return err
			}
		}

		if err := applyOne(ctx, c, u); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(o.Log, "applied %d objects\n", len(docs))

	if err := waitDeployments(ctx, c, ns, waitFor); err != nil {
		return err
	}

	// The CiliumNetworkPolicies go last, once the workloads are Ready, so a default-deny cannot
	// cut the bootstrap off. The published bundle carries them too.
	return applyPolicies(ctx, c, o.Component, ns)
}

func policiesFor(component string) ([]*unstructured.Unstructured, error) {
	raw, err := stack.Policies(component)
	if err != nil {
		return nil, err
	}

	return parse(raw)
}

func applyPolicies(ctx context.Context, c client.Client, component, ns string) error {
	docs, err := policiesFor(component)
	if err != nil {
		return err
	}

	for _, u := range docs {
		if u.GetNamespace() == "" {
			u.SetNamespace(ns)
		}

		if err := applyOne(ctx, c, u); err != nil {
			return err
		}
	}

	return nil
}

func parse(manifest []byte) ([]*unstructured.Unstructured, error) {
	rd := yamlutil.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifest)))

	var out []*unstructured.Unstructured

	for {
		doc, err := rd.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}

		if err != nil {
			return nil, fmt.Errorf("splitting the manifest: %w", err)
		}

		obj := map[string]any{}
		if err := sigsyaml.Unmarshal(doc, &obj); err != nil {
			return nil, fmt.Errorf("parsing the manifest: %w", err)
		}

		if len(obj) > 0 {
			out = append(out, &unstructured.Unstructured{Object: obj})
		}
	}
}

func namespaceObject(n string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": kindNamespace, metaKey: map[string]any{"name": n},
	}}
}

// defaultNamespace puts a namespaced object that names no namespace into ns.
func defaultNamespace(ctx context.Context, c client.Client, u *unstructured.Unstructured, ns string) error {
	gvk := u.GroupVersionKind()

	var mapping *meta.RESTMapping

	err := retryNoMatch(ctx, func() error {
		var err error

		mapping, err = c.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)

		return err
	})
	if err != nil {
		return fmt.Errorf("mapping %s: %w", u.GetKind(), err)
	}

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		u.SetNamespace(ns)
	}

	return nil
}

func applyOne(ctx context.Context, c client.Client, u *unstructured.Unstructured) error {
	err := retryNoMatch(ctx, func() error {
		return c.Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
			client.FieldOwner(FieldManager), client.ForceOwnership)
	})
	if err != nil {
		return fmt.Errorf("applying %s %s: %w", u.GetKind(), u.GetName(), err)
	}

	return nil
}

// retryNoMatch retries f while it reports a kind that is not served yet: a CRD applied a
// moment ago needs a moment before its kind can be applied.
func retryNoMatch(ctx context.Context, f func() error) error {
	for {
		err := f()
		if !meta.IsNoMatchError(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("a kind was never served: %w", err)
		case <-time.After(retryInterval):
		}
	}
}

// createSecret creates s in its namespace and never touches it again: these Secrets carry
// generated values (and Argo CD fills argocd-secret itself), so a rerun must not reset them.
// They are not part of any bundle, so Argo CD does not track them; the annotations make that
// explicit in case a bundle ever names one.
func createSecret(ctx context.Context, c client.Client, u *unstructured.Unstructured, defaultNS string) error {
	var s corev1.Secret
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &s); err != nil {
		return err
	}

	if s.Namespace == "" {
		s.Namespace = defaultNS
	}

	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}

	s.Annotations[annotationPrune] = "Prune=false"
	s.Annotations[annotationCompare] = "IgnoreExtraneous"
	s.Annotations[managedByAnnotation] = JobLabel
	s.ResourceVersion = ""

	if err := c.Create(ctx, &s); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating Secret %s/%s: %w", s.Namespace, s.Name, err)
	}

	return nil
}

func waitDeployments(ctx context.Context, c client.Client, ns string, names []string) error {
	for _, n := range names {
		for {
			var d appsv1.Deployment

			err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: n}, &d)
			if err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("reading Deployment %s/%s: %w", ns, n, err)
			}

			want := int32(1)
			if d.Spec.Replicas != nil {
				want = *d.Spec.Replicas
			}

			if err == nil && d.Status.ReadyReplicas >= want {
				break
			}

			select {
			case <-ctx.Done():
				return fmt.Errorf("deployment %s/%s did not become Ready: %w", ns, n, ctx.Err())
			case <-time.After(retryInterval):
			}
		}
	}

	return nil
}
