#!/usr/bin/env bash

yq_path="pkg/provider/providers.yaml"

# resolve_release_version resolves a Go pseudo-version to the nearest prior
# GitHub release tag. Go pseudo-versions (vX.Y.Z-REVISION.YYYYMMDDHHMMSS-HASH)
# are commit-based and have no release artifacts, so this finds the closest
# released tag. Example:
# v0.5.13-0.20260306160140-6254d39cf77c -> v0.5.13 exist?:
# - YES, return v0.5.13
# - NO, return v0.5.12 (nearest prior release)
# Returns the original version unchanged if it is not a pseudo-version.
resolve_release_version() {
  local repo="$1"
  local version="$2"
  local release_url="https://api.github.com/repos/${repo}/releases/tags"

  # Go pseudo-versions: vX.Y.Z-REVISION.YYYYMMDDHHMMSS-12HEXCHARS
  if [[ ! "$version" =~ ^(v[0-9]+\.[0-9]+\.[0-9]+)-[0-9]+\.[0-9]{14}-[a-f0-9]{12}$ ]]; then
    echo "$version"
    return 0
  fi

  local base="${BASH_REMATCH[1]}"
  echo "Pseudo-version detected ($version), resolving release tag for $repo" >&2

  # Check if the base semver tag has a corresponding GitHub release
  local http_status
  http_status=$(curl -s -o /dev/null -w "%{http_code}" \
    "${release_url}/${base}")
  if [ "$http_status" = "200" ]; then
    echo "Resolved pseudo-version to $base" >&2
    echo "$base"
    return 0
  fi

  # Decrement patch version and check if that release exists (e.g. v0.5.13 -> v0.5.12)
  local major minor patch prior
  IFS='.' read -r major minor patch <<< "${base#v}"
  prior="v${major}.${minor}.$((patch - 1))"

  http_status=$(curl -s -o /dev/null -w "%{http_code}" "${release_url}/${prior}")
  if [ "$http_status" = "200" ]; then
    echo "Resolved pseudo-version to prior release $prior" >&2
    echo "$prior"
    return 0
  fi

  echo "Error: could not find a release tag for $repo@$version" >&2
  exit 1
}

rm -f pkg/provider/crds/*.yaml
rm -f pkg/provider/webhooks/*.yaml

count=$(yq '.providers | length' "$yq_path")
for i in $(seq 0 $((count - 1))); do
  name=$(yq -r ".providers[$i].name" "$yq_path")
  provider=$(yq -r ".providers[$i].provider" "$yq_path")
  repo=$(yq ".providers[$i].repository" "$yq_path")
  go_module=$(yq -r ".providers[$i].go_module" "$yq_path")
  file=$(yq ".providers[$i].file_name" "$yq_path")
  filter=$(yq -r ".providers[$i].filter" "$yq_path")
  development_mode=$(yq -r ".providers[$i].development_mode" "$yq_path")

  if [ "$development_mode" == "true" ] && [ "$KOMMODITY_DEVELOPMENT_MODE" != "true" ]; then
    echo "Skipping $name as it is only for development mode"
    continue
  fi

  if [ -n "$go_module" ] && [ "$go_module" != "null" ]; then
    version=$(go mod graph | grep "$go_module" | head -n1 | awk -F'@' '{print $2}')
  else
    version=$(go mod graph | grep "$repo" | head -n1 | awk -F'@' '{print $2}')
  fi

  version=$(resolve_release_version "$repo" "$version")

  # Fetch CRD manifests
  if [ "$file" == "null" ]; then
    echo "'file' field is null. Skipping CRD manifests for $name."
    continue
  fi
  url="https://github.com/${repo}/releases/download/${version}/$file"

  echo "Fetching from $url with filter $filter"

  curl -sL "$url" -o "pkg/provider/${name}.yaml"


  if [ -n "$filter" ]; then
    yq eval "$filter" "pkg/provider/${name}.yaml" | yq -s '"pkg/provider/crds/\(.spec.names.kind).yaml"'
  fi

  for kind in $(yq -r ".providers[$i].deny_list[]" "$yq_path"); do
    rm -f "pkg/provider/crds/${kind}.yaml"
  done

  mkdir -p "pkg/provider/crds/${provider}"
  for crdfile in pkg/provider/crds/*.yaml; do
    [ -e "$crdfile" ] || continue
    mv "$crdfile" "pkg/provider/crds/${provider}/"
  done

  rm -f "pkg/provider/${name}.yaml"

  # Fetch webhooks if present
  webhook_count=$(yq ".providers[$i].webhooks | length" "$yq_path")
  if [ "$webhook_count" != "null" ] && [ "$webhook_count" -gt 0 ]; then
    for j in $(seq 0 $((webhook_count - 1))); do
      webhook_path=$(yq -r ".providers[$i].webhooks[$j]" "$yq_path")

      # Compose raw github URL for webhook manifest
      webhook_url="https://raw.githubusercontent.com/${repo}/refs/tags/${version}/${webhook_path}"

      echo "Fetching webhook manifest from $webhook_url"

      curl -sL "$webhook_url" -o "pkg/provider/${name}-webhook.yaml"

      # Split webhook manifest into individual YAMLs
      yq '(.metadata.name |= "'${name}'-" + .)' "pkg/provider/${name}-webhook.yaml" | yq -s '"pkg/provider/webhooks/\(.metadata.name).yaml"'

      rm -f "pkg/provider/${name}-webhook.yaml"
    done
  fi
done
