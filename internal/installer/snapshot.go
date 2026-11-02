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

package installer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// SnapshotLabel marks every ConfigMap of a snapshot; its value is the run id.
	SnapshotLabel = "ichor.maesh.dev/installer-snapshot"

	// snapshotChunkBytes keeps each ConfigMap far below the 1 MiB object limit; a gzip of
	// the seven CRDs is a few hundred KiB, so usually one chunk, but the size of a CRD is not
	// something to bet the rollback on.
	snapshotChunkBytes = 512 * 1024

	snapshotKey = "part"

	// snapshotTakenAnnotation orders snapshots for garbage collection: creationTimestamp has
	// one-second resolution, and several runs can land in the same second.
	snapshotTakenAnnotation = SnapshotLabel + "-taken"

	// snapshotStageAnnotation says which stage of the run's snapshot a ConfigMap belongs to.
	snapshotStageAnnotation = SnapshotLabel + "-stage"

	// SnapshotsKept is how many snapshots (the current one included) survive a run: enough to
	// go back past the last upgrade, few enough that ConfigMaps do not pile up.
	SnapshotsKept = 3
)

// entry is one object of the snapshot. A nil Object records that the object did not exist,
// so a rollback deletes what the upgrade created.
type entry struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Object     map[string]any `json:"object,omitempty"`
}

func snapshotName(runID string, stage, i int) string {
	if stage == 0 {
		return runID + "-snapshot-" + strconv.Itoa(i)
	}

	return runID + "-snapshot-s" + strconv.Itoa(stage) + "-" + strconv.Itoa(i)
}

// takeSnapshot saves the live state of every object of one stage of the plan. A run snapshots in
// stages (the installer's own RBAC first, everything else once that RBAC is effective),
// all under one run id, and a rollback reads them back as one list. A stage that already exists
// for runID is kept as it is and returned: a re-run after a crash would otherwise snapshot the
// half-upgraded cluster over the good one.
//
// A NotFound, or a kind whose CRD is not installed yet, means the object does not exist and
// there is nothing to restore. Any other error, Forbidden included, fails: a rollback depends on
// the snapshot being complete.
func takeSnapshot(ctx context.Context, c client.Client, ns, runID string, stage int, objs []*unstructured.Unstructured) ([]entry, error) {
	if existing, found, err := loadStage(ctx, c, ns, runID, stage); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}

	entries := make([]entry, 0, len(objs))

	for _, o := range objs {
		e := entry{APIVersion: o.GetAPIVersion(), Kind: o.GetKind(), Namespace: o.GetNamespace(), Name: o.GetName()}

		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(o.GroupVersionKind())

		err := c.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: o.GetName()}, live)

		switch {
		case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		case err != nil:
			return nil, fmt.Errorf("snapshotting %s %s: %w", o.GetKind(), o.GetName(), err)
		default:
			e.Object = stripLive(live.Object)
		}

		entries = append(entries, e)
	}

	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	data := buf.Bytes()
	taken := time.Now().UTC().Format(time.RFC3339Nano)
	parts := (len(data) + snapshotChunkBytes - 1) / snapshotChunkBytes

	for i := range parts {
		chunk := data[i*snapshotChunkBytes : min((i+1)*snapshotChunkBytes, len(data))]

		cm := &corev1.ConfigMap{
			Namespace: ns,
			Name:      snapshotName(runID, stage, i),
			Labels:    map[string]string{SnapshotLabel: runID},
			Annotations: map[string]string{
				SnapshotLabel + "-parts": strconv.Itoa(parts),
				snapshotStageAnnotation:  strconv.Itoa(stage),
				snapshotTakenAnnotation:  taken,
			},
			BinaryData: map[string][]byte{snapshotKey: chunk},
		}
		if err := c.Create(ctx, cm); err != nil {
			return nil, fmt.Errorf("saving snapshot part %d of %d: %w", i+1, parts, err)
		}
	}

	return entries, nil
}

// loadSnapshot reads the snapshot saved for runID (every stage, in order), or nil when there is none.
func loadSnapshot(ctx context.Context, c client.Client, ns, runID string) ([]entry, error) {
	var all []entry

	for stage := 0; ; stage++ {
		entries, found, err := loadStage(ctx, c, ns, runID, stage)
		if err != nil {
			return nil, err
		}

		if !found {
			return all, nil
		}

		all = append(all, entries...)
	}
}

// loadStage reads one stage of the snapshot saved for runID. A snapshot made before stages
// existed has no stage annotation and is stage 0.
func loadStage(ctx context.Context, c client.Client, ns, runID string, stage int) ([]entry, bool, error) {
	var list corev1.ConfigMapList
	if err := c.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{SnapshotLabel: runID}); err != nil {
		return nil, false, err
	}

	var parts []corev1.ConfigMap

	for _, cm := range list.Items {
		if st, _ := strconv.Atoi(cm.Annotations[snapshotStageAnnotation]); st == stage {
			parts = append(parts, cm)
		}
	}

	if len(parts) == 0 {
		return nil, false, nil
	}

	slices.SortFunc(parts, func(a, b corev1.ConfigMap) int { return strings.Compare(a.Name, b.Name) })

	want, _ := strconv.Atoi(parts[0].Annotations[SnapshotLabel+"-parts"])
	if want != len(parts) {
		return nil, false, fmt.Errorf("snapshot %s is incomplete: %d of %d parts found; it cannot be trusted to roll back",
			runID, len(parts), want)
	}

	var data []byte
	for _, cm := range parts {
		data = append(data, cm.BinaryData[snapshotKey]...)
	}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, false, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	var entries []entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	return entries, true, nil
}

// stripLive removes what the API server owns, so the saved object can be written back.
func stripLive(obj map[string]any) map[string]any {
	u := (&unstructured.Unstructured{Object: obj}).DeepCopy()
	u.SetUID("")
	u.SetResourceVersion("")
	u.SetGeneration(0)
	u.SetCreationTimestamp(metav1.Time{})
	u.SetManagedFields(nil)
	u.SetSelfLink("")
	unstructured.RemoveNestedField(u.Object, "status")

	return u.Object
}

// pruneSnapshots deletes every snapshot except the keep newest, and never the one named
// current. A snapshot is all the ConfigMaps carrying one run id.
func pruneSnapshots(ctx context.Context, c client.Client, ns, current string, keep int) error {
	var list corev1.ConfigMapList
	if err := c.List(ctx, &list, client.InNamespace(ns), client.HasLabels{SnapshotLabel}); err != nil {
		return err
	}

	taken := map[string]string{}
	parts := map[string][]*corev1.ConfigMap{}

	for i := range list.Items {
		cm := &list.Items[i]
		id := cm.Labels[SnapshotLabel]
		parts[id] = append(parts[id], cm)

		if t := cm.Annotations[snapshotTakenAnnotation]; t > taken[id] {
			taken[id] = t
		}
	}

	ids := make([]string, 0, len(parts))
	for id := range parts {
		ids = append(ids, id)
	}

	// Newest first. A snapshot without a timestamp (made before this existed) sorts oldest.
	slices.SortFunc(ids, func(a, b string) int {
		if d := strings.Compare(taken[b], taken[a]); d != 0 {
			return d
		}

		return strings.Compare(b, a)
	})

	for i, id := range ids {
		if i < keep || id == current {
			continue
		}

		for _, cm := range parts[id] {
			if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting old snapshot %s: %w", id, err)
			}
		}
	}

	return nil
}
