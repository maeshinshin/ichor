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
	"io"
)

// Human output of rotate ca: the credential hand-off reports on stderr as it goes;
// what is printed here is the end of the command. An empty kubeconfig means no credential hand-off
// (the etcd or aggregator CA alone): your kubeconfig still works, so nothing points at a new one.

// printRotateCAStarted ends a rotate ca without --follow: the operation runs on, the new
// kubeconfig is the one that reaches the cluster.
func printRotateCAStarted(out io.Writer, opName, cluster, kubeconfig string) error {
	h := newHuman(out)

	h.done("started ca-rotation operation %s", opName)
	h.Line("follow with: ichor describe operation " + opName + " --watch" + kubeconfigFlag(kubeconfig))
	rotateCAAfterwardsHuman(h, "When it completes", cluster, kubeconfig)

	return h.Err()
}

// printRotateCADone ends a rotate ca --follow once the operation completed.
func printRotateCADone(out io.Writer, opName, cluster, kubeconfig string) error {
	h := newHuman(out)

	h.done("ca-rotation operation %s completed", opName)
	rotateCAAfterwardsHuman(h, "Next", cluster, kubeconfig)

	return h.Err()
}

func kubeconfigFlag(kubeconfig string) string {
	if kubeconfig == "" {
		return ""
	}

	return " --kubeconfig " + kubeconfig
}

// rotateCAAfterwardsHuman is rotateCAAfterwards as lines: the old kubeconfig trusts only the old CA,
// so the credentials command is pointed at the new one.
func rotateCAAfterwardsHuman(h *human, title, cluster, kubeconfig string) {
	h.Note("")
	h.Title(title)

	if kubeconfig != "" {
		h.Line("ichor create credentials " + cluster + kubeconfigFlag(kubeconfig) + h.Paint(sgrDim, "   files that trust the new CA only"))
	}

	h.Line("ichor export bundle " + cluster + h.Paint(sgrDim, "   save the new secrets bundle"))
}
