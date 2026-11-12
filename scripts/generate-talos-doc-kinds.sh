#!/usr/bin/env bash

# Generates charts/kommodity-cluster/files/talos-doc-kinds.yaml from the JSON
# machine-config schema Talos publishes for the chart's pinned version
# (Chart.yaml appVersion). A document kind is "named" when its schema
# definition has a "name" property, otherwise a "singleton". The v1alpha1.Config
# root definition (no "kind" property) is excluded.

set -euo pipefail

chart_file="charts/kommodity-cluster/Chart.yaml"
out_file="charts/kommodity-cluster/files/talos-doc-kinds.yaml"

app_version=$(awk '/^appVersion:/ {gsub(/"/, "", $2); print $2; exit}' "$chart_file")
if [ -z "${app_version:-}" ]; then
  echo "error: could not read appVersion from $chart_file" >&2
  exit 1
fi
tag="v${app_version}"

schema_url="https://raw.githubusercontent.com/siderolabs/talos/${tag}/pkg/machinery/config/schemas/config.schema.json"
echo "Fetching Talos machine-config schema for ${tag}"

schema=$(curl -fsSL --retry 2 "$schema_url") || {
  echo "error: could not fetch $schema_url" >&2
  exit 1
}

# Abort on a malformed schema before writing anything: emit to a temp file,
# move into place only on success.
tmp_file=$(mktemp)
trap 'rm -f "$tmp_file"' EXIT

if ! jq -e '
  [ .oneOf[]."$ref" | ltrimstr("#/$defs/") ] as $refs |
  [ ."$defs" | to_entries[] | select(.key as $k | $refs | index($k)) ] |
  map(select(.value.properties.kind)) |
  {
    named: (map(select(.value.properties.name)) | map(.value.properties.kind.enum[0]) | sort),
    singleton: (map(select(.value.properties.name | not)) | map(.value.properties.kind.enum[0]) | sort)
  } | (.named | length > 0) and (.singleton | length > 0)
' <<<"$schema" > /dev/null 2>&1; then
  echo "error: schema from $schema_url is malformed or has an unexpected shape" >&2
  exit 1
fi

jq -r '
  [ .oneOf[]."$ref" | ltrimstr("#/$defs/") ] as $refs |
  [ ."$defs" | to_entries[] | select(.key as $k | $refs | index($k)) ] |
  map(select(.value.properties.kind)) |
  {
    named: (map(select(.value.properties.name)) | map(.value.properties.kind.enum[0]) | sort),
    singleton: (map(select(.value.properties.name | not)) | map(.value.properties.kind.enum[0]) | sort)
  } |
  "namedKinds:",
  (.named[] | "  \(.): true"),
  "singletonKinds:",
  (.singleton[] | "  \(.): {}")
' <<<"$schema" > "$tmp_file"

if ! grep -q "namedKinds" "$tmp_file"; then
  echo "error: generated classification is empty; refusing to write $out_file" >&2
  exit 1
fi

mv "$tmp_file" "$out_file"
trap - EXIT
echo "Wrote $out_file"
