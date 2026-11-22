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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// ADR 0173: a storage preset adds the system extensions it needs to spec.schematic and creates the upgrade
// that applies them, paused until a person resumes it.
const (
	reasonStorageExtensionsMissing     = "ExtensionsMissing"
	reasonStorageSchematicNotMergeable = "SchematicNotMergeable"
	reasonStorageSchematicUnavailable  = "SchematicUnavailable"

)

// SchematicRegistrar turns a set of official system extensions into a schematic ID at the Image Factory
// whose base URL is factoryURL (TalosCluster.spec.imageFactoryURL).
type SchematicRegistrar interface {
	Register(ctx context.Context, factoryURL string, extensions []string) (string, error)
}

// ImageFactoryClient registers schematics at Image Factory (POST /schematics, Talos v1.14 "Boot Assets").
// The ID is a function of the content, so a repeated registration returns the same ID; it is remembered per process.
type ImageFactoryClient struct {
	// HTTP is the client; nil means one with a 30 second timeout.
	HTTP *http.Client

	mu   sync.Mutex
	seen map[string]string
}

// Register implements SchematicRegistrar.
func (c *ImageFactoryClient) Register(ctx context.Context, factoryURL string, extensions []string) (string, error) {
	key := factoryURL + "|" + strings.Join(extensions, ",")

	c.mu.Lock()
	id, ok := c.seen[key]
	c.mu.Unlock()

	if ok {
		return id, nil
	}

	var body strings.Builder

	body.WriteString("customization:\n  systemExtensions:\n    officialExtensions:\n")

	for _, e := range extensions {
		body.WriteString("      - " + e + "\n")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(factoryURL, "/")+"/schematics", strings.NewReader(body.String()))
	if err != nil {
		return "", err
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("registering a schematic at Image Factory: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	var out struct {
		ID string `json:"id"`
	}

	if resp.StatusCode/100 != 2 || json.Unmarshal(raw, &out) != nil || out.ID == "" {
		return "", fmt.Errorf("the Image Factory answered %s to the schematic: %.200s", resp.Status, raw)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.seen == nil {
		c.seen = map[string]string{}
	}

	c.seen[key] = out.ID

	return out.ID, nil
}

// ensureExtensions is the whole of ADR 0173 for one TalosStorage whose governed nodes lack extensions. It returns
// the reason and message to report; the message names the missing extensions' remedy. With no Actor or Factory it
// does nothing and returns an empty reason, which leaves the plain MissingExtensions message.
func (r *TalosStorageReconciler) ensureExtensions(
	ctx context.Context, ts *ichorv1alpha1.TalosStorage, preset storage.Preset, problems string,
) (reason, msg string, err error) {
	if r.Actor == nil || r.Factory == nil {
		return "", "", nil
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: ts.Namespace, Name: ts.Spec.ClusterRef.Name}, &cluster); err != nil {
		return "", "", client.IgnoreNotFound(err)
	}

	want := slices.Sorted(slices.Values(preset.RequiredExtensions()))

	id, rerr := r.Factory.Register(ctx, cluster.Spec.FactoryURL(), want)
	if rerr != nil {
		return reasonStorageSchematicUnavailable, fmt.Sprintf("%s: %s. Image Factory %s could not be asked for a schematic with them: %v",
			ts.Spec.DiskPreset, problems, cluster.Spec.FactoryURL(), rerr), nil
	}

	switch cur := targetSchematic(&cluster); cur {
	case id:
	case upgrade.DefaultSchematic:
		patch := fmt.Sprintf(`{"spec":{"schematic":{"id":%q}}}`, id)
		if err := r.Actor.Patch(ctx, &cluster, client.RawPatch(types.MergePatchType, []byte(patch))); err != nil {
			return "", "", fmt.Errorf("writing spec.schematic.id: %w", err)
		}
	default:
		return reasonStorageSchematicNotMergeable, fmt.Sprintf(
			"%s: %s. spec.schematic.id is %s, a schematic whose content Ichor cannot read, so it adds nothing to it. "+
				"Build a schematic that includes %s, set spec.schematic.id to it and run ichor upgrade talos",
			ts.Spec.DiskPreset, problems, cur, strings.Join(want, ", ")), nil
	}

	state, err := r.ensureExtensionsOperation(ctx, &cluster, id)
	if err != nil {
		return "", "", err
	}

	return reasonStorageExtensionsMissing, fmt.Sprintf("%s: %s. The Talos upgrade that adds %s %s",
		ts.Spec.DiskPreset, problems, strings.Join(want, ", "), state), nil
}

// ensureExtensionsOperation creates the paused upgrade for schematic id, once: its name derives from the cluster and the ID, so
// a second pass, or a second controller, finds it instead of making another. It returns the sentence that says where it is.
func (r *TalosStorageReconciler) ensureExtensionsOperation(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, id string) (string, error) {
	sum := sha256.Sum256([]byte(cluster.Name + "/" + id))
	op := &ichorv1alpha1.TalosOperation{
		Namespace: cluster.Namespace, Name: "storage-extensions-" + hex.EncodeToString(sum[:])[:10],
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindTalos, Paused: true,
		},
	}
	op.Annotations = map[string]string{
		ichorv1alpha1.OperationStorageExtensionsAnnotation: id,
		ichorv1alpha1.OperationStartedByAnnotation:         ichorv1alpha1.StorageExtensionsStarter,
	}

	err := r.Create(ctx, op)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating the extensions upgrade: %w", err)
	}

	if err == nil {
		return awaitingApproval(op), nil
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(op), op); err != nil {
		return awaitingApproval(op), client.IgnoreNotFound(err) // not in the cache yet: it was just created
	}

	switch {
	case op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted:
		return fmt.Sprintf("(operation %s) completed, but the nodes do not report them yet", op.Name), nil
	case len(op.Status.Machines) > 0 && !op.Spec.Paused:
		return fmt.Sprintf("(operation %s) is running", op.Name), nil
	case op.Status.FailureReason != "":
		return fmt.Sprintf("(operation %s) failed: %s", op.Name, op.Status.FailureReason), nil
	}

	return awaitingApproval(op), nil
}

func awaitingApproval(op *ichorv1alpha1.TalosOperation) string {
	return fmt.Sprintf("(operation %s) is waiting for approval: ichor resume operation %s", op.Name, op.Name)
}
