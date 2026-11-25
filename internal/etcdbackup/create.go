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
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The keys of the S3 credentials Secret the controller reads (spec.destination.s3.credentialsSecretRef).
const (
	S3KeyAccessKeyID     = "accessKeyID"
	S3KeySecretAccessKey = "secretAccessKey"
	S3KeySessionToken    = "sessionToken"
	S3KeyCA              = "ca.crt"
)

// The patterns of the CRD (api/v1alpha1.TalosEtcdBackupS3), so a request is refused before it leaves the machine.
var (
	endpointPattern = regexp.MustCompile(`^https?://[^/\s]+$`)
	prefixPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

// NewBackup is what `ichor create etcd-backup` asks for: one TalosEtcdBackup and the Secret it names.
type NewBackup struct {
	Name, Namespace, Cluster string
	Endpoint, Region         string
	Bucket, Prefix           string
	Insecure                 bool
	// Schedule and KeepLast left at their zero value take the CRD's defaults.
	Schedule string
	KeepLast int32
	// SecretName is the credentials Secret, which lives in the controller's namespace, not Namespace.
	SecretName string
}

// Validate applies the CRD's and the admission webhook's rules to n, so the operator gets the refusal
// before a Secret is written.
func (n NewBackup) Validate() error {
	var errs []error

	if n.Name == "" || n.Cluster == "" || n.SecretName == "" {
		errs = append(errs, errors.New("a name, a cluster and a credentials Secret name are required"))
	}

	if !endpointPattern.MatchString(n.Endpoint) {
		errs = append(errs, fmt.Errorf("endpoint %q must be a URL like https://s3.example.com", n.Endpoint))
	}

	if strings.HasPrefix(strings.ToLower(n.Endpoint), "http://") && !n.Insecure {
		errs = append(errs, errors.New("endpoint is a plain http:// URL, which sends the access key id in clear; use https:// or pass --s3-insecure"))
	}

	if len(n.Bucket) < 3 {
		errs = append(errs, fmt.Errorf("bucket %q must be at least 3 characters (and must already exist)", n.Bucket))
	}

	if !prefixPattern.MatchString(n.Prefix) {
		errs = append(errs, fmt.Errorf("prefix %q must be letters, digits, '.', '_' or '-', with '/' only between parts and none at either end", n.Prefix))
	}

	if n.KeepLast < 0 {
		errs = append(errs, errors.New("keep-last must be at least 1"))
	}

	if n.Schedule != "" {
		if _, err := cron.ParseStandard(n.Schedule); err != nil {
			errs = append(errs, fmt.Errorf("schedule is not a five-field cron expression: %w", err))
		}
	}

	return errors.Join(errs...)
}

// Backup is the TalosEtcdBackup for n.
func (n NewBackup) Backup() *ichorv1alpha1.TalosEtcdBackup {
	return &ichorv1alpha1.TalosEtcdBackup{
		Name: n.Name, Namespace: n.Namespace,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{
			ClusterRef: corev1.LocalObjectReference{Name: n.Cluster},
			Schedule:   n.Schedule,
			Retention:  ichorv1alpha1.TalosEtcdBackupRetention{KeepLast: n.KeepLast},
			Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
				Endpoint: n.Endpoint, Insecure: n.Insecure, Region: n.Region, Bucket: n.Bucket, Prefix: n.Prefix,
				CredentialsSecretRef: corev1.LocalObjectReference{Name: n.SecretName},
			}},
		},
	}
}

// CredentialsSecret is the Secret the controller reads the S3 credentials from; namespace is the controller's.
func CredentialsSecret(namespace, name string, c S3Config) *corev1.Secret {
	data := map[string][]byte{
		S3KeyAccessKeyID:     []byte(c.AccessKeyID),
		S3KeySecretAccessKey: []byte(c.SecretAccessKey),
	}

	if c.SessionToken != "" {
		data[S3KeySessionToken] = []byte(c.SessionToken)
	}

	if len(c.CAPEM) > 0 {
		data[S3KeyCA] = c.CAPEM
	}

	return &corev1.Secret{Name: name, Namespace: namespace, Type: corev1.SecretTypeOpaque, Data: data}
}
