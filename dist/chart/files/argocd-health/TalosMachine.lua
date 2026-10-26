-- Health of an Ichor TalosMachine for Argo CD.
-- Reads only what internal/controller writes: status.conditions[Ready] and
-- status.observedGeneration. Ready=False with these two reasons is a wait, not a failure.
local hs = {}
local waiting = { ClusterLockHeld = true, ClusterTransitioning = true }

if obj.metadata ~= nil and obj.metadata.deletionTimestamp ~= nil then
  hs.status = "Progressing"
  hs.message = "being removed"
  return hs
end

local ready = nil
if obj.status ~= nil and obj.status.conditions ~= nil then
  for _, c in ipairs(obj.status.conditions) do
    if c.type == "Ready" then ready = c end
  end
end

if ready == nil then
  hs.status = "Progressing"
  hs.message = "waiting for the first reconcile"
  return hs
end

local gen = obj.metadata and obj.metadata.generation or 0
local seen = obj.status.observedGeneration or 0
if seen < gen then
  hs.status = "Progressing"
  hs.message = "spec generation " .. gen .. " not reconciled yet (observed " .. seen .. ")"
  return hs
end

hs.message = (ready.reason or "") .. ": " .. (ready.message or "")
if ready.status == "True" then
  hs.status = "Healthy"
elseif waiting[ready.reason] then
  hs.status = "Progressing"
else
  hs.status = "Degraded"
end
return hs
