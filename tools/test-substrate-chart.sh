#!/usr/bin/env bash
set -euo pipefail

# Requires Helm, Python 3, and PyYAML.
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

render() {
    local chart=$1
    shift
    helm template obot "$chart" --namespace obot --set dev.useEmbeddedDb=true --include-crds "$@"
}

render chart > "$work/disabled.yaml"
if render chart --set substrate.enabled=true > "$work/missing.yaml" 2> "$work/missing.err"; then
    echo 'Expected rendering without an image repository to fail' >&2
    exit 1
fi
grep -q 'substrate.image.repository is required' "$work/missing.err"

flags=(--set substrate.enabled=true --set substrate.image.repository=registry.example.com/substrate
    --set substrate.storage.existingSecret=snapshots --set substrate.postgres.storageSize=20Gi)
render chart "${flags[@]}" > "$work/enabled.yaml"
dsn='postgresql://substrate:test-password@database.example.com/substrate?sslmode=require&connect_timeout=10'
render chart "${flags[@]}" --set-string "substrate.postgres.dsn=$dsn" > "$work/external.yaml"

helm package chart --destination "$work"
archives=("$work"/*.tgz)
render "${archives[0]}" "${flags[@]}" > "$work/packaged.yaml"

python3 - "$work" "$dsn" <<'PY'
import json
from pathlib import Path
import sys
import yaml

work = Path(sys.argv[1])
dsn = sys.argv[2]
namespaces = {'ate-system', 'podcertificate-controller-system'}


def read(name):
    text = (work / f'{name}.yaml').read_text()
    for placeholder in ('ko://', '${SUBSTRATE_VERSION', '${ENVOY_DATAPLANE_IMAGE}'):
        assert placeholder not in text, f'Unresolved placeholder: {placeholder}'
    return [doc for doc in yaml.safe_load_all(text) if doc]


def resource(docs, kind, name):
    matches = [doc for doc in docs if doc['kind'] == kind and doc['metadata']['name'] == name]
    assert len(matches) == 1, f'Missing or duplicate {kind}/{name}'
    return matches[0]


def has_secret(workload, name):
    sources = workload['spec']['template']['spec']['containers'][0]['envFrom']
    return any(entry.get('secretRef', {}).get('name') == name for entry in sources)


disabled = read('disabled')
assert not any(doc['metadata'].get('namespace') in namespaces for doc in disabled)
assert not any(doc['kind'] == 'CustomResourceDefinition' for doc in disabled)

docs = read('enabled')
assert sum(doc['kind'] == 'CustomResourceDefinition' for doc in docs) == 3
for name in ('ate-api-server', 'ate-controller', 'atenet-router', 'atenet-egress', 'podcertificate-controller'):
    assert resource(docs, 'Deployment', name)['metadata']['namespace'] in namespaces
for name in namespaces:
    assert resource(docs, 'Namespace', name)['metadata']['annotations']['helm.sh/resource-policy'] == 'keep'

bootstrap = resource(docs, 'Job', 'substrate-bootstrap-1')
assert not bootstrap['metadata'].get('annotations')
assert [container['args'][2] for container in bootstrap['spec']['template']['spec']['initContainers']] == [
    'podcertificate-controller-cas', 'jwt-authority-pool', 'actor-id-ca-pool', 'actor-id-ca-certs']

api = resource(docs, 'Deployment', 'ate-api-server')
atelet = resource(docs, 'DaemonSet', 'atelet-v0-3-0')
assert atelet['spec']['template']['spec']['nodeSelector']['kubernetes.io/os'] == 'linux'
assert all(has_secret(workload, 'snapshots') for workload in (api, atelet))

postgres = resource(docs, 'StatefulSet', 'postgres')
assert postgres['spec']['volumeClaimTemplates'][0]['spec']['resources']['requests']['storage'] == '20Gi'
assert any(volume.get('projected', {}).get('sources', [{}])[0].get('podCertificate') is not None
           for volume in postgres['spec']['template']['spec']['volumes'])

external = read('external')
assert not any(doc['metadata'].get('namespace') == 'ate-system' and
               doc['metadata']['name'] in ('postgres', 'postgres-config') for doc in external)
assert resource(external, 'Secret', 'ate-api-server-secret-envvars')['stringData']['ATE_API_POSTGRES_CONNECTION_STRING'] == dsn
assert resource(external, 'ConfigMap', 'ate-api-server-envvars')['data'].get('ATE_API_POSTGRES_CONNECTION_STRING') is None
assert dsn not in json.dumps([doc for doc in external if doc['kind'] == 'ConfigMap'])
external_api = resource(external, 'Deployment', 'ate-api-server')
assert has_secret(external_api, 'ate-api-server-secret-envvars')
assert external_api['spec']['template']['metadata']['annotations']['checksum/postgres-dsn'] != api['spec']['template']['metadata']['annotations']['checksum/postgres-dsn']

# Obot generates random keys at render time; compare Substrate resources.
def substrate(docs):
    return [doc for doc in docs if doc['metadata'].get('namespace') in namespaces or
            doc['kind'] in ('CustomResourceDefinition', 'SandboxConfig')]

assert substrate(read('packaged')) == substrate(docs), 'Packaged Substrate resources differ'
print('Substrate chart checks passed (disabled, enabled, external PostgreSQL, bootstrap, storage, CRDs, package).')
PY
