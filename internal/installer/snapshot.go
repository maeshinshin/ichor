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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func snapshotName(runID string, i int) string { return runID + "-snapshot-" + strconv.Itoa(i) }

// takeSnapshot saves the live state of every object the plan will touch. A snapshot that
// already exists for runID is kept as it is and returned: a re-run after a crash would
// otherwise snapshot the half-upgraded cluster over the good one.
func takeSnapshot(ctx context.Context, c client.Client, ns, runID string, objs []*unstructured.Unstructured) ([]entry, error) {
	if existing, err := loadSnapshot(ctx, c, ns, runID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	entries := make([]entry, 0, len(objs))

	for _, o := range objs {
		e := entry{APIVersion: o.GetAPIVersion(), Kind: o.GetKind(), Namespace: o.GetNamespace(), Name: o.GetName()}

		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(o.GroupVersionKind())

		err := c.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: o.GetName()}, live)

		switch {
		case apierrors.IsNotFound(err):
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
	parts := (len(data) + snapshotChunkBytes - 1) / snapshotChunkBytes

	for i := range parts {
		chunk := data[i*snapshotChunkBytes : min((i+1)*snapshotChunkBytes, len(data))]

		cm := &corev1.ConfigMap{
			Namespace:   ns,
			Name:        snapshotName(runID, i),
			Labels:      map[string]string{SnapshotLabel: runID},
			Annotations: map[string]string{SnapshotLabel + "-parts": strconv.Itoa(parts)},
			BinaryData:  map[string][]byte{snapshotKey: chunk},
		}
		if err := c.Create(ctx, cm); err != nil {
			return nil, fmt.Errorf("saving snapshot part %d of %d: %w", i+1, parts, err)
		}
	}

	return entries, nil
}

// loadSnapshot reads the snapshot saved for runID, or nil when there is none.
func loadSnapshot(ctx context.Context, c client.Client, ns, runID string) ([]entry, error) {
	var list corev1.ConfigMapList
	if err := c.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{SnapshotLabel: runID}); err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	slices.SortFunc(list.Items, func(a, b corev1.ConfigMap) int { return strings.Compare(a.Name, b.Name) })

	want, _ := strconv.Atoi(list.Items[0].Annotations[SnapshotLabel+"-parts"])
	if want != len(list.Items) {
		return nil, fmt.Errorf("snapshot %s is incomplete: %d of %d parts found; it cannot be trusted to roll back",
			runID, len(list.Items), want)
	}

	var data []byte
	for _, cm := range list.Items {
		data = append(data, cm.BinaryData[snapshotKey]...)
	}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	var entries []entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", runID, err)
	}

	return entries, nil
}

// stripLive removes what the API server owns, so the saved object can be written back.
func stripLive(obj map[string]any) map[string]any {
	u := &unstructured.Unstructured{Object: runtimeCopy(obj)}
	u.SetUID("")
	u.SetResourceVersion("")
	u.SetGeneration(0)
	u.SetCreationTimestamp(metav1.Time{})
	u.SetManagedFields(nil)
	u.SetSelfLink("")
	unstructured.RemoveNestedField(u.Object, "status")

	return u.Object
}

func runtimeCopy(obj map[string]any) map[string]any {
	return (&unstructured.Unstructured{Object: obj}).DeepCopy().Object
}
