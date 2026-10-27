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

package etcdbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotFound is what Store.Get returns, wrapped, for a key that does not exist.
var ErrNotFound = errors.New("object not found")

// FetchVerified is the restore recipe: Get, Decrypt, check the etcd checksum trailer. The
// plaintext is held in memory rather than on disk, since it contains every Secret in the cluster.
func FetchVerified(ctx context.Context, store Store, key, passphrase string) (*bytes.Buffer, error) {
	var buf bytes.Buffer

	if _, err := stream(ctx, store, key, passphrase, &buf); err != nil {
		return nil, err
	}

	return &buf, nil
}

// VerifyObject runs the same recipe as FetchVerified without keeping the plaintext: nothing of it is held
// beyond one copy buffer, so a snapshot of any size can be checked. It returns the plaintext size.
func VerifyObject(ctx context.Context, store Store, key, passphrase string) (int64, error) {
	return stream(ctx, store, key, passphrase)
}

// stream downloads and decrypts key, checks the checksum trailer, copies the plaintext to each of sinks
// and returns its size.
func stream(ctx context.Context, store Store, key, passphrase string, sinks ...io.Writer) (int64, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("downloading: %w", err)
	}

	defer func() { _ = rc.Close() }()

	plain, err := Decrypt(rc, passphrase)
	if err != nil {
		return 0, fmt.Errorf("decrypting (is this the cluster's own secrets bundle?): %w", err)
	}

	v := NewVerifier()

	if _, err := io.Copy(io.MultiWriter(append([]io.Writer{v}, sinks...)...), plain); err != nil {
		return 0, fmt.Errorf("decrypting: %w", err)
	}

	return v.Size(), v.Verify()
}

// Reader opens key as plaintext. It is not verified as it streams: call VerifyObject first. The caller
// closes it.
func Reader(ctx context.Context, store Store, key, passphrase string) (io.ReadCloser, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("downloading: %w", err)
	}

	plain, err := Decrypt(rc, passphrase)
	if err != nil {
		_ = rc.Close()

		return nil, fmt.Errorf("decrypting (is this the cluster's own secrets bundle?): %w", err)
	}

	return struct {
		io.Reader
		io.Closer
	}{plain, rc}, nil
}

// Names of the two objects an etcd restore keeps next to the snapshots, under the same prefix. List
// skips them (they are not ObjectNames), so retention never prunes them and no other tool takes them
// for a snapshot.
const (
	// MarkerName holds the RestoreMarker.
	MarkerName = "etcd-restore.marker.json"
	// PinnedName is the ciphertext copy of the snapshot being restored, taken before anything is wiped, so
	// pruning cannot take it away mid-restore.
	PinnedName = "etcd-restore.snapshot.age"
)

// Steps of a RestoreMarker. They are the same words as TalosOperation.status.etcdRestore.step.
const (
	MarkerWiping     = "Wiping"
	MarkerRecovering = "Recovering"
	MarkerRejoining  = "Rejoining"
)

// RestoreMarker is the state of an etcd restore that must outlive the Kubernetes API. A restore rewinds
// the cluster's etcd to the snapshot, and the TalosOperation that drives it was created after the
// snapshot, so the rewind deletes it. The marker lives in the backup bucket, which a rewind cannot
// touch, and holds enough to recreate the operation and to know how far the destructive part got. It
// holds no secret: names, times and the break-glass reason.
type RestoreMarker struct {
	Namespace string `json:"namespace"`
	Operation string `json:"operation"`
	Cluster   string `json:"cluster"`
	Backup    string `json:"backup"`
	Snapshot  string `json:"snapshot"`
	// Reason is the break-glass reason the restore was started under.
	Reason       string    `json:"reason"`
	SnapshotTime time.Time `json:"snapshotTime"`
	// Target is the control plane the snapshot is recovered on.
	Target string `json:"target"`
	// Step is one of the Marker* constants: how far the destructive part got. It only moves forward.
	Step string `json:"step"`
	// Failure is why the restore stopped, if it did; set while the step is not finished.
	Failure string `json:"failure,omitempty"`
	// LastNote is the job's last transient problem ("dialing cp-a: ..."), kept so that a process that
	// died leaves a trace; it is not final.
	LastNote string `json:"lastNote,omitempty"`
	// Nodes maps each control plane's TalosMachine name to its Kubernetes node name, read before
	// anything was wiped (a node cannot be asked afterwards). Own is the machine the controller runs on,
	// which is wiped last; empty when the controller runs elsewhere.
	Nodes map[string]string `json:"nodes,omitempty"`
	Own   string            `json:"own,omitempty"`
	// OwnWipeAt is when the job recorded that it was about to wipe the controller's own node (Own), before
	// it sent the wipe; OwnDone is set once that node is known to be wiped or a member. The wipe is done by
	// the job through the Talos API alone, because the node's etcd is stale and the Kubernetes API is not
	// reachable from it.
	OwnWipeAt time.Time `json:"ownWipeAt,omitzero"`
	OwnDone   bool      `json:"ownDone,omitempty"`
	// AgentsSince is when the kubelets of the nodes that were not wiped were restarted (the node-side
	// agents refresh); zero before. It bounds the wait for them and makes the restart once.
	AgentsSince time.Time `json:"agentsSince,omitzero"`
	// AgentWarning is what had not converged on those nodes when the wait for them ran out.
	AgentWarning string    `json:"agentWarning,omitempty"`
	StartedAt    time.Time `json:"startedAt"`
}

// MarkerKey and PinnedKey are the object keys under a backup's prefix.
func MarkerKey(prefix string) string { return prefix + "/" + MarkerName }

// PinnedKey: see PinnedName.
func PinnedKey(prefix string) string { return prefix + "/" + PinnedName }

// PutMarker writes m.
func PutMarker(ctx context.Context, store Store, prefix string, m *RestoreMarker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}

	return store.Put(ctx, MarkerKey(prefix), bytes.NewReader(b), int64(len(b)))
}

// GetMarker reads the marker; it returns (nil, nil) when there is none.
func GetMarker(ctx context.Context, store Store, prefix string) (*RestoreMarker, error) {
	rc, err := store.Get(ctx, MarkerKey(prefix))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil //nolint:nilnil // "no marker" is a normal answer
		}

		return nil, fmt.Errorf("reading the restore marker: %w", err)
	}

	defer func() { _ = rc.Close() }()

	var m RestoreMarker
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("decoding the restore marker: %w", err)
	}

	return &m, nil
}

// DeleteMarker removes the marker.
func DeleteMarker(ctx context.Context, store Store, prefix string) error {
	return store.Delete(ctx, MarkerKey(prefix))
}

// Pin copies the ciphertext at key to the pinned object under prefix, size bytes long.
func Pin(ctx context.Context, store Store, prefix, key string, size int64) error {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("downloading the snapshot to pin it: %w", err)
	}

	defer func() { _ = rc.Close() }()

	return store.Put(ctx, PinnedKey(prefix), rc, size)
}
