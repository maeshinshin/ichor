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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// `ichor init --merge-kubeconfig` (docs/adr/0134) merges the cluster-admin kubeconfig into the
// user's own kubeconfig. It only adds entries: a name that is taken by different content gets a
// suffix, and nothing existing is changed or removed.

// kubeconfigMergeTarget returns the file a merge writes to and every file of the loading chain
// (read for name collisions). The target is client-go's write target, the one kubectl uses for new
// entries: the explicit path, else the first existing file of $KUBECONFIG, else its first entry,
// else ~/.kube/config.
func kubeconfigMergeTarget(explicit string) (target string, chain []string) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = explicit

	return rules.GetDefaultFilename(), rules.GetLoadingPrecedence()
}

// loadKubeconfigFile loads path, or an empty config when it does not exist.
func loadKubeconfigFile(path string) (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return clientcmdapi.NewConfig(), nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig %s: %w", path, err)
	}

	return cfg, nil
}

// mergeKubeconfig merges incoming (the cluster-admin kubeconfig of cluster) into target as cluster
// `cluster`, user and context `admin@cluster` (a free suffix on the cluster name if those are taken
// by other content in any file of chain), sets the current context to it, and writes target
// atomically at 0600. It returns the context name. Credentials are never part of an error.
func mergeKubeconfig(target string, chain []string, incoming []byte, cluster string) (string, error) {
	in, err := clientcmd.Load(incoming)
	if err != nil {
		return "", fmt.Errorf("parsing the cluster's kubeconfig: %w", err)
	}

	src, ok := in.Contexts[in.CurrentContext]
	if !ok || in.Clusters[src.Cluster] == nil || in.AuthInfos[src.AuthInfo] == nil {
		return "", errors.New("the cluster's kubeconfig has no usable current context")
	}

	cl, user := in.Clusters[src.Cluster], in.AuthInfos[src.AuthInfo]

	dst, err := loadKubeconfigFile(target)
	if err != nil {
		return "", err
	}

	taken := clientcmdapi.NewConfig()

	for _, p := range chain {
		other, err := loadKubeconfigFile(p)
		if err != nil {
			return "", err
		}

		for k, v := range other.Clusters {
			taken.Clusters[k] = v
		}

		for k, v := range other.AuthInfos {
			taken.AuthInfos[k] = v
		}

		for k, v := range other.Contexts {
			taken.Contexts[k] = v
		}
	}

	name, userName := cluster, "admin@"+cluster

	for i := 1; ; i++ {
		if sameKubeconfigEntry(dst, name, userName, cl, user) {
			break // a repeated merge: already there
		}

		if taken.Clusters[name] == nil && taken.AuthInfos[userName] == nil && taken.Contexts[userName] == nil {
			break
		}

		name = fmt.Sprintf("%s-%d", cluster, i)
		userName = "admin@" + name
	}

	dst.Clusters[name] = cl
	dst.AuthInfos[userName] = user
	dst.Contexts[userName] = &clientcmdapi.Context{Cluster: name, AuthInfo: userName, Namespace: src.Namespace}
	dst.CurrentContext = userName

	out, err := clientcmd.Write(*dst)
	if err != nil {
		return "", fmt.Errorf("encoding the merged kubeconfig: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(target), bundleDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}

	if err := writeFileAtomic(target, out); err != nil {
		return "", fmt.Errorf("writing %s: %w", target, err)
	}

	return userName, nil
}

// sameKubeconfigEntry reports whether cfg already holds the cluster, user and context under these
// names with this content.
func sameKubeconfigEntry(cfg *clientcmdapi.Config, cluster, user string, cl *clientcmdapi.Cluster, au *clientcmdapi.AuthInfo) bool {
	c, a, x := cfg.Clusters[cluster], cfg.AuthInfos[user], cfg.Contexts[user]
	if c == nil || a == nil || x == nil || x.Cluster != cluster || x.AuthInfo != user {
		return false
	}

	return reflect.DeepEqual(withoutOrigin(c), withoutOrigin(cl)) && reflect.DeepEqual(withoutOrigin(a), withoutOrigin(au))
}

func withoutOrigin[T clientcmdapi.Cluster | clientcmdapi.AuthInfo](v *T) *T {
	cp := *v

	switch p := any(&cp).(type) {
	case *clientcmdapi.Cluster:
		p.LocationOfOrigin = ""
	case *clientcmdapi.AuthInfo:
		p.LocationOfOrigin = ""
	}

	return &cp
}
