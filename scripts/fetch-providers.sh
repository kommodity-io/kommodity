#!/usr/bin/env bash

yq_path="pkg/provider/providers.yaml"

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

  # Resolve the provider version from the module graph
  if [ -n "$go_module" ] && [ "$go_module" != "null" ]; then
    version=$(go mod graph | awk '{print $2}' | grep -F "${go_module}@" | head -n1 | awk -F'@' '{print $2}')
  else
    version=$(go mod graph | awk '{print $2}' | grep -F "/${repo}@" | head -n1 | awk -F'@' '{print $2}')
  fi

  if [ -z "$version" ]; then
    echo "Could not resolve version for $name (go_module='$go_module', repo='$repo'). Skipping."
    continue
  fi

  # Fetch CRD manifests, either from individual files at the git tag (crd_paths)
  # or from a release asset (file_name).
  crd_count=$(yq ".providers[$i].crd_paths | length" "$yq_path")
  if [ "$crd_count" != "null" ] && [ "$crd_count" -gt 0 ]; then
    for j in $(seq 0 $((crd_count - 1))); do
      crd_path=$(yq -r ".providers[$i].crd_paths[$j]" "$yq_path")
      crd_url="https://raw.githubusercontent.com/${repo}/refs/tags/${version}/${crd_path}"

      echo "Fetching CRD manifest from $crd_url"

      # Raw CRD bases lack the Cluster API contract label added by kustomize at
      # release time, so stamp it with all served API versions of the CRD.
      curl -sL "$crd_url" |
        yq '.metadata.labels."cluster.x-k8s.io/v1beta1" = ([.spec.versions[].name] | join("_"))' |
        yq -s '"pkg/provider/crds/\(.spec.names.kind).yaml"'
    done
  elif [ "$file" == "null" ]; then
    echo "'file' field is null. Skipping CRD manifests for $name."
    continue
  else
    url="https://github.com/${repo}/releases/download/${version}/$file"

    echo "Fetching from $url with filter $filter"

    curl -sL "$url" -o "pkg/provider/${name}.yaml"

    if [ -n "$filter" ]; then
      yq eval "$filter" "pkg/provider/${name}.yaml" | yq -s '"pkg/provider/crds/\(.spec.names.kind).yaml"'
    fi
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
