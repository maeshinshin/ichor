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

import "github.com/pkg/browser"

// openBrowser opens url in the platform's default browser. github.com/pkg/browser
// is already pulled in indirectly (through k8s.io/kubectl); using it directly
// means uiDeps.OpenBrowser's real implementation is one line instead of a
// hand-rolled, three-way os/exec switch duplicating what it already does.
func openBrowser(url string) error {
	return browser.OpenURL(url)
}
