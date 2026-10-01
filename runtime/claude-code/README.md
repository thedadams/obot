# Claude Code / Substrate POC

The bundled [Substrate chart](../../chart/charts/substrate/README.md) deploys
alongside Obot in the same Helm upgrade on a compatible cluster. Configure an Anthropic-compatible model in Obot and
build Obot from this checkout. The default Obot release image does not contain
this POC.

Build the runtime for your worker architectures and publish it (Nushell):

```nu
docker buildx build --platform linux/amd64,linux/arm64 --tag registry.example.com/obot-claude:poc --push runtime/claude-code
```

Use the digest printed by the build in your Obot Helm values:

```yaml
substrate:
  enabled: true
  image:
    repository: registry.example.com/substrate
  poc:
    enabled: true
    namespace: obot-agents
    image: registry.example.com/obot-claude@sha256:REPLACE_WITH_DIGEST
    snapshotLocation: s3://obot-snapshots/agents/
    replicas: 2
    # Defaults to Obot's in-cluster Service. Override with a DNS URL when needed.
    # obotURL: https://obot.example.com
```

Keep your existing Obot database, image, and Substrate storage credential values.
`substrate.postgres.dsn` still selects an external Substrate database; leaving it
empty deploys bundled PostgreSQL. The snapshot bucket must already exist and be
accessible to Substrate's API and atelet. Runtime images must be readable by
atelet (anonymous pulls, or GCP credentials with
`substrate.atelet.gcpAuthForImagePulls=true`). Kubernetes image pull Secrets alone
do not authenticate atelet's actor-image downloads. For private worker images,
configure image pull credentials for the worker namespace's service account.

```nu
helm upgrade --install obot ./chart --namespace obot --create-namespace --values obot-values.yaml --wait --wait-for-jobs --timeout 10m
```


For an existing S3/MinIO service, no values file or separately created Kubernetes
Secret is required. Append these arguments to your Obot command using Nushell's
argument spreading (`...$substrate_args`):

```nu
let substrate_args = [
  --set substrate.enabled=true
  --set substrate.poc.enabled=true
  --set substrate.image.repository=ghcr.io/thedadams/substrate
  --set-string "substrate.poc.image=ghcr.io/thedadams/obot-claude@sha256:REPLACE_WITH_DIGEST"
  --set substrate.poc.replicas=1
  --set-string "substrate.poc.snapshotLocation=s3://YOUR_BUCKET/agents/"
  --set-string "substrate.storage.env.AWS_ENDPOINT_URL=http://YOUR_MINIO_HOST:9000"
  --set-string substrate.storage.env.AWS_S3_USE_PATH_STYLE=true
  --set-literal "substrate.storage.credentials.AWS_ACCESS_KEY_ID=YOUR_ACCESS_KEY"
  --set-literal "substrate.storage.credentials.AWS_SECRET_ACCESS_KEY=YOUR_SECRET_KEY"
  --wait --wait-for-jobs --timeout 10m
]
```

The bucket must exist. Omit the endpoint and path-style overrides for AWS S3,
and set `substrate.storage.env.AWS_REGION` if it differs from `us-east-1`.
Helm installs the CRDs on upgrade, creates namespaces and credentials, and
initializes the custom resources after the CRDs are established. No node labels
are required with the default Linux node selector. Kubernetes certificate
feature gates remain a prerequisite configured on the cluster itself.

Open **AI Resources → Agents (POC)**. Create an instance with a Claude model and
optional comma-separated MCP server or vMCP IDs you already have access to. Wait
for `RUNNING`, then chat. `Suspend` drains the current turn and snapshots the
workspace; `Resume` restores its files and Claude session. `Stop turn` cancels
the active query. Deleting an instance revokes its key and removes its actor and
template. Deleting a user also schedules deletion of their instances.

## What this implements

- One shared Kubernetes worker namespace, one atespace per Obot owner, and one
  actor and private template per instance. Kubernetes only manages the worker
  infrastructure; the Obot controller uses Substrate's native control API.
- Owner-checked Obot APIs and chat proxy. Browsers never receive Substrate
  credentials or connect to actors directly.
- An instance-specific scoped Obot API key, stored using Obot's credential
  encryption configuration, for the LLM gateway and selected MCP servers.
  Provisioning the key and storing its secret is one database transaction.
- A Claude Agent SDK HTTP runtime with one conversation and one active turn per
  instance. Model/MCP access is reauthorized by the existing Obot gateway.
- Durable `/workspace` files, including the SDK session and a bounded UI
  transcript. Data snapshots cold-boot the HTTP service on resume; no process
  memory or open connections are restored, and no golden snapshots are used.

The runtime permits file and shell tools within the sandbox without an approval
dialog. Egress permits only the configured Obot DNS host and port. Arbitrary
internet access, repository cloning from external hosts, and package downloads
are outside this POC. Model and MCP selections are immutable; create another
instance to change them. Existing instances retain their pinned runtime image
when chart values change.

There is no automatic idle suspension, sharing, multiple chats, terminal, or
crash recovery UI. Chat streams inherit Substrate's five-minute route timeout;
interrupted turns are not automatically replayed. Suspend before maintenance:
the most recent completed Substrate snapshot is the durable recovery point,
not every chat message. Empty owner atespaces are retained after instance
deletion. Do not disable the POC before deleting instances, since that disables
their reconciliation and cleanup.

This implementation has compile, type, syntax, and chart-render validation;
automated tests and a live-cluster end-to-end run are still needed.
