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
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// renderRestoreStarted is the result of `ichor restore --live` without --follow: the operation that
// carries the restore out, and how to watch it.
func renderRestoreStarted(h *human, resp *pb.CreateOperationResponse) error {
	ref := resp.GetOperation().GetRef()
	if err := doneLine(h, "started etcd restore operation %s", nsName(ref)); err != nil {
		return err
	}

	h.Line("follow it with: ichor operation get " + ref.GetName() + " --watch")

	return h.Err()
}
