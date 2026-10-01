#!/usr/bin/env bash
set -euo pipefail

# Publishes upstream images; requires Go 1.27+, ko, make, curl, and Docker buildx.
# Authenticate to your registry before running this script.
if [[ $# -ne 1 || -z $1 ]]; then
    echo "Usage: $0 REGISTRY_REPOSITORY" >&2
    exit 1
fi

repository=$1
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
chart="$script_dir/../chart/charts/substrate/Chart.yaml"
# Read the unquoted version and commit fields maintained in this chart.
version=$(awk '$1 == "appVersion:" { print $2 }' "$chart")
revision=$(awk '$1 == "substrate.obot.ai/upstream-commit:" { print $2 }' "$chart")
if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || ! $revision =~ ^[a-f0-9]{40}$ ]]; then
    echo "Invalid appVersion or upstream commit in $chart" >&2
    exit 1
fi

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

curl --fail --location "https://api.github.com/repos/agent-substrate/substrate/tarball/$revision" --output "$work/source.tar.gz"
tar -xzf "$work/source.tar.gz" -C "$work" --strip-components=1

cd "$work"
export KO_DOCKER_REPO="$repository"
export KO_DEFAULTPLATFORMS="linux/amd64,linux/arm64"

# Use upstream's ko configuration and Envoy Dockerfile.
# No demo or microVM image is needed for this chart's gVisor runtime.
make build-images build-envoy-dataplane "KO=ko" \
    "KO_DOCKER_REPO=$repository" "VERSION=$version" "KO_TAGS=--tags=$version" \
    "IMAGES=./cmd/ateapi ./cmd/atecontroller ./cmd/atelet ./cmd/atenet ./cmd/podcertcontroller ./cmd/ateom-gvisor"

# ate-setup's create commands only need a repository marker, not source or
# build tools. ko mounts kodata at /var/run/ko, the Job's working directory.
mkdir -p cmd/ate-setup/kodata
cp go.mod cmd/ate-setup/kodata/go.mod
ko build --base-import-paths "--tags=$version" \
    "--ldflags=-X=github.com/agent-substrate/substrate/internal/version.Version=$version" ./cmd/ate-setup
