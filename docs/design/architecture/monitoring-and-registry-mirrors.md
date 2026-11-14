---
sidebar_group: "Integrations"
sidebar_order: 19
---

# Monitoring and registry mirrors

Ichor does not install a monitoring stack. It can expose the control-plane
metrics that Talos binds locally by default (controller-manager, scheduler,
etcd) and restrict scraping to a CIDR with Talos's ingress firewall. The
controller and UI backend export Prometheus metrics, and ServiceMonitors,
alert rules and Grafana dashboards ship with the chart as an option. The
monitoring stack itself is a documented set of Helm values, or nothing — the
user's choice.

Registry mirrors are optional and off by default: they are pushed to every
node through the system layer, alongside an override for the installer image
source and a self-hosted Image Factory URL. They serve pull-through caches and
private registries, not air-gapped installs. Ichor does not check
that a mirror is reachable before an upgrade.
