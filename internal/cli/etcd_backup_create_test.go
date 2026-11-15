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

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const (
	ebAccessKey = "AKIAEXAMPLE"
	ebSecretKey = "example-secret-key-value"
)

func ebEnv(k string) string {
	return map[string]string{"ICHOR_S3_ACCESS_KEY_ID": ebAccessKey, "ICHOR_S3_SECRET_ACCESS_KEY": ebSecretKey}[k]
}

func ebFlags() etcdbackup.NewBackup {
	return etcdbackup.NewBackup{Cluster: "adopt-cluster", Endpoint: "https://s3.example.com", Bucket: "ichor-backups"}
}

func TestCreateEtcdBackupWritesTheSecretInTheControllerNamespaceAndTheBackup(t *testing.T) {
	o, creds, err := planEtcdBackup("nightly", testNamespace, ebFlags(), "", ebEnv, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(newTestCluster()).Build()

	var out, errw bytes.Buffer

	if err := runCreateEtcdBackup(context.Background(), k8s, &out, &errw, o, creds, ichorControllerNamespace); err != nil {
		t.Fatal(err)
	}

	var sec corev1.Secret
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: "nightly-s3"}, &sec); err != nil {
		t.Fatalf("the credentials Secret is not in the controller's namespace: %v", err)
	}

	if string(sec.Data["accessKeyID"]) != ebAccessKey || string(sec.Data["secretAccessKey"]) != ebSecretKey || len(sec.Data) != 2 {
		t.Fatalf("Secret keys = %v", sec.Data)
	}

	var bk ichorv1alpha1.TalosEtcdBackup
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "nightly"}, &bk); err != nil {
		t.Fatal(err)
	}

	s3 := bk.Spec.Destination.S3
	if bk.Spec.ClusterRef.Name != "adopt-cluster" || s3.CredentialsSecretRef.Name != "nightly-s3" || s3.Prefix != "adopt-cluster" || s3.Bucket != "ichor-backups" {
		t.Fatalf("spec = %+v", bk.Spec)
	}

	if strings.Contains(out.String()+errw.String(), ebSecretKey) || strings.Contains(out.String()+errw.String(), ebAccessKey) {
		t.Fatal("the credentials were printed")
	}

	// Neither object is overwritten by a second run.
	if err := runCreateEtcdBackup(context.Background(), k8s, &out, &errw, o, creds, ichorControllerNamespace); err == nil {
		t.Fatal("a second run overwrote the backup")
	}
}

func TestCreateEtcdBackupOfAnUnknownClusterWritesNoSecret(t *testing.T) {
	o, creds, err := planEtcdBackup("nightly", testNamespace, ebFlags(), "", ebEnv, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}

	// No cluster of that name: refused before anything is written.
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var out, errw bytes.Buffer

	if err := runCreateEtcdBackup(context.Background(), k8s, &out, &errw, o, creds, ichorControllerNamespace); err == nil {
		t.Fatal("a backup of an unknown cluster was created")
	}

	var secrets corev1.SecretList
	if err := k8s.List(context.Background(), &secrets, client.InNamespace(ichorControllerNamespace)); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("a Secret was left behind: %v %v", err, secrets.Items)
	}
}

func TestCreateEtcdBackupRemovesTheSecretWhenTheBackupIsRefused(t *testing.T) {
	o, creds, err := planEtcdBackup("nightly", testNamespace, ebFlags(), "", ebEnv, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}

	base := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(newTestCluster()).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosEtcdBackup); ok {
				return errors.New("admission webhook refused")
			}

			return c.Create(ctx, obj, opts...)
		},
	})

	var out, errw bytes.Buffer

	if err := runCreateEtcdBackup(context.Background(), k8s, &out, &errw, o, creds, ichorControllerNamespace); err == nil {
		t.Fatal("the refusal was swallowed")
	}

	var secrets corev1.SecretList
	if err := base.List(context.Background(), &secrets, client.InNamespace(ichorControllerNamespace)); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("a Secret was left behind: %v %v (stderr %q)", err, secrets.Items, errw.String())
	}
}

// The CLI refuses what the CRD and the admission webhook would refuse, before it writes the Secret.
func TestCreateEtcdBackupRefusesBeforeTouchingTheCluster(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*etcdbackup.NewBackup)
		want   string
	}{
		"http without insecure": {func(o *etcdbackup.NewBackup) { o.Endpoint = "http://minio.example.com:9000" }, "plain http://"},
		"a missing bucket":      {func(o *etcdbackup.NewBackup) { o.Bucket = "" }, "bucket"},
		"a leading slash":       {func(o *etcdbackup.NewBackup) { o.Prefix = "/prod" }, "prefix"},
		"a trailing slash":      {func(o *etcdbackup.NewBackup) { o.Prefix = "prod/" }, "prefix"},
		"a bad schedule":        {func(o *etcdbackup.NewBackup) { o.Schedule = "every hour" }, "schedule"},
	} {
		t.Run(name, func(t *testing.T) {
			o := ebFlags()
			tc.mutate(&o)

			_, _, err := planEtcdBackup("nightly", testNamespace, o, "", ebEnv, os.ReadFile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	o := ebFlags()
	o.Endpoint = "http://minio.example.com:9000"
	o.Insecure = true

	if _, _, err := planEtcdBackup("nightly", testNamespace, o, "", ebEnv, os.ReadFile); err != nil {
		t.Fatalf("http with insecure: %v", err)
	}

	if _, _, err := planEtcdBackup("nightly", testNamespace, ebFlags(), "", func(string) string { return "" }, os.ReadFile); err == nil ||
		!strings.Contains(err.Error(), "ICHOR_S3_ACCESS_KEY_ID") {
		t.Fatalf("no credentials: %v", err)
	}
}
