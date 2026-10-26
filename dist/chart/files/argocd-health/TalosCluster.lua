-- Health of an Ichor TalosCluster for Argo CD. The cluster has no Ready condition: nothing
-- writes one, and CNIHandover=False is a normal state (Disabled, ManualPending), so neither
-- may turn it unhealthy. The one signal that means "still moving" is Transitioning=True
-- (an even control-plane count during a replacement). Per-node health is on TalosMachine.
local hs = {}
hs.status = "Healthy"
hs.message = "accepted; per-node health is reported by its TalosMachines"

if obj.status ~= nil and obj.status.conditions ~= nil then
  for _, c in ipairs(obj.status.conditions) do
    if c.type == "Transitioning" and c.status == "True" then
      hs.status = "Progressing"
      hs.message = c.message or "control plane count is changing"
    end
  end
end
return hs
