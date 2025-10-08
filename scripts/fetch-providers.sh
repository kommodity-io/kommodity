#!/usr/bin/env bash

yq_path="pkg/provider/providers.yaml"

rm -f pkg/provider/crds/*.yaml
rm -f pkg/provider/webhooks/*.yaml

count=$(yq '.providers | length' "$yq_path")
for i in $(seq 0 $((count - 1))); do
  name=$(yq -r ".providers[$i].name" "$yq_path")
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
  
  # Fetch CRD manifests
  url="https://github.com/${repo}/releases/download/${version}/$file"
  
  echo "Fetching from $url with filter $filter"

  curl -sL "$url" -o "pkg/provider/${name}.yaml"
  
  if [ -n "$filter" ]; then
    yq eval "$filter" "pkg/provider/${name}.yaml" | yq -s '"pkg/provider/crds/\(.spec.names.kind).yaml"'
  fi

  for kind in $(yq -r ".providers[$i].deny_list[]" "$yq_path"); do
    rm -f "pkg/provider/crds/${kind}.yaml"
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
