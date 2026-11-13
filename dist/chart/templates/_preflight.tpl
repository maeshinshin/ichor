{{/*
ichor.preflight.noCNI takes a list of Node objects (what lookup returns under .items) and prints
"true" when there is at least one node and every node is NotReady with the network-plugin message
(`cni plugin not initialized` / reason NetworkPluginNotReady): a cluster with no CNI.
Any Ready node, or any node NotReady for another reason, prints nothing.
*/}}
{{- define "ichor.preflight.noCNI" -}}
{{- $stuck := 0 -}}
{{- range . -}}
{{- range ((.status | default dict).conditions | default list) -}}
{{- if and (eq .type "Ready") (ne .status "True") -}}
{{- $m := lower (printf "%s %s" (.reason | default "") (.message | default "")) -}}
{{- if or (contains "networkpluginnotready" $m) (contains "cni plugin not initialized" $m) -}}
{{- $stuck = add1 $stuck -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and (gt (len .) 0) (eq $stuck (len .)) -}}true{{- end -}}
{{- end }}

{{/*
ichor.preflight fails the install on a cluster with no pod network. lookup returns nothing under
`helm template` and `--dry-run`, so the check passes there; preflight.cni=false turns it off.
*/}}
{{- define "ichor.preflight" -}}
{{- if .Values.preflight.cni -}}
{{- $nodes := lookup "v1" "Node" "" "" -}}
{{- if and $nodes $nodes.items (eq (include "ichor.preflight.noCNI" $nodes.items) "true") -}}
{{- fail "this cluster has no CNI, so no pod can start, including Ichor's. Run `ichor bootstrap cni` first (it installs Ichor's Cilium), then run helm install again; see docs/guides/adopt-and-reclaim.md. Set preflight.cni=false to skip this check." -}}
{{- end -}}
{{- end -}}
{{- end }}
