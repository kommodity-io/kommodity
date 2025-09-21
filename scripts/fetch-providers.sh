#!/usr/bin/env bash

yq_path="pkg/provider/providers.yaml"

rm -f pkg/provider/crds/*.yaml

count=$(yq '.providers | length' "$yq_path")
for i in $(seq 0 $((count - 1))); do
  name=$(yq -r ".providers[$i].name" "$yq_path")
  repo=$(yq ".providers[$i].repository" "$yq_path")
  go_module=$(yq -r ".providers[$i].go_module" "$yq_path")
  file=$(yq ".providers[$i].file_name" "$yq_path")
  filter=$(yq -r ".providers[$i].filter" "$yq_path")
  version_override=$(yq -r ".providers[$i].version_override" "$yq_path")

  if [ -n "$go_module" ] && [ "$go_module" != "null" ]; then
    version=$(go mod graph | grep "$go_module" | head -n1 | awk -F'@' '{print $2}')
  else
    version=$(go mod graph | grep "$repo" | head -n1 | awk -F'@' '{print $2}')
  fi

  if [ -n "$version_override" ] && [ "$version_override" != "null" ]; then
    version="$version_override"
  fi
  
  url="https://${repo}/releases/download/${version}/$file"
  
  echo "Fetching from $url with filter $filter"

  curl -sL "$url" -o "pkg/provider/${name}.yaml"
  
  if [ -n "$filter" ]; then
    yq eval "$filter" "pkg/provider/${name}.yaml" | yq -s '"pkg/provider/crds/\(.spec.names.kind).yaml"'
  fi

  for kind in $(yq -r ".providers[$i].deny_list[]" "$yq_path"); do
    rm -f "pkg/provider/crds/${kind}.yaml"
  done

  rm -f "pkg/provider/${name}.yaml"
done
