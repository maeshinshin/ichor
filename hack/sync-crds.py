#!/usr/bin/env python3
"""Sync CRD schemas from config/crd/bases/ into dist/chart/templates/crd/.

Replaces each chart CRD file's "spec:" block (controller-gen's output: group, names,
scope, versions/schema) with the matching file's from config/crd/bases/, byte for byte.
Everything around it -- the "{{- if .Values.crd.enabled }}" wrapper, the
"{{- if .Values.crd.keep }}" annotation templating, and the trailing "{{- end }}" -- is
left untouched, since none of that exists in config/ for this script to copy from.

Run via "make sync-crds" (CONTRIBUTING.md explains why this replaced
"kubebuilder edit --plugins=helm/v2-alpha" for the whole chart).
"""

import pathlib
import re
import sys

CONFIG_DIR = pathlib.Path("config/crd/bases")
CHART_DIR = pathlib.Path("dist/chart/templates/crd")

SPEC_LINE = re.compile(r"^spec:$", re.MULTILINE)
END_LINE = re.compile(r"^\{\{- end \}\}\s*$", re.MULTILINE)


def chart_filename(config_path: pathlib.Path) -> str:
    # config/crd/bases names files "<group>_<plural>.yaml"; the chart names the same
    # CRD "<plural>.<group>.yaml". Both are controller-gen/kubebuilder's own naming
    # conventions, not something this script invents.
    group, plural = config_path.stem.split("_", 1)
    return f"{plural}.{group}.yaml"


def spec_section(text: str) -> str:
    m = SPEC_LINE.search(text)
    if not m:
        sys.exit(f"no top-level 'spec:' line found")
    return text[m.start() :]


def sync_one(config_path: pathlib.Path) -> None:
    chart_path = CHART_DIR / chart_filename(config_path)
    if not chart_path.exists():
        sys.exit(
            f"{chart_path} does not exist; a new CRD needs its chart wrapper "
            "(the {{- if .Values.crd.enabled }}/metadata templating) written by hand "
            "once, modeled on an existing file in dist/chart/templates/crd/, before "
            "this script can sync its spec"
        )

    new_spec = spec_section(config_path.read_text())

    chart_text = chart_path.read_text()
    spec_match = SPEC_LINE.search(chart_text)
    end_match = END_LINE.search(chart_text)
    if not spec_match or not end_match:
        sys.exit(f"{chart_path}: could not find both 'spec:' and '{{{{- end }}}}' lines")

    chart_path.write_text(chart_text[: spec_match.start()] + new_spec + chart_text[end_match.start() :])
    print(f"synced {chart_path} from {config_path}")


def main() -> None:
    for config_path in sorted(CONFIG_DIR.glob("*.yaml")):
        sync_one(config_path)


if __name__ == "__main__":
    main()
