#!/usr/bin/env bash
# Validate every example configuration against the current provider schema.
#
# Builds the provider, points terraform at it via dev_overrides (so no
# registry download or `terraform init` is needed), and runs
# `terraform validate` in a copy of each example directory. Example dirs
# that don't declare required_providers get a synthesized versions.tf.
#
# This is the check that catches examples referencing attributes the
# provider no longer has.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Building provider..."
go build -o "$work/bin/terraform-provider-dataversecontact" "$repo_root"

cat > "$work/cli.tfrc" <<EOF
provider_installation {
  dev_overrides {
    "TrueNorthIT/dataversecontact" = "$work/bin"
  }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE="$work/cli.tfrc"

fail=0
for dir in "$repo_root"/examples/provider "$repo_root"/examples/full-scope \
  "$repo_root"/examples/resources/* "$repo_root"/examples/data-sources/*; do
  [ -d "$dir" ] || continue
  name="${dir#"$repo_root"/}"
  scratch="$work/scratch-$(echo "$name" | tr '/' '-')"
  mkdir -p "$scratch"
  cp "$dir"/*.tf "$scratch"/

  if ! grep -rq "required_providers" "$scratch"; then
    cat > "$scratch/versions.tf" <<EOF
terraform {
  required_providers {
    dataversecontact = {
      source = "TrueNorthIT/dataversecontact"
    }
  }
}
EOF
  fi

  # With dev_overrides, validate runs against the local build without init.
  if out="$(cd "$scratch" && terraform validate 2>&1)"; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    echo "$out" | sed 's/^/     /'
    fail=1
  fi
done

exit $fail
