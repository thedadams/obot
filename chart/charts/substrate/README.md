# Agent Substrate for Obot

This bundled subchart packages [agent-substrate/substrate v0.3.0](https://github.com/agent-substrate/substrate/tree/ccecc788a327dc11dcd6c21ee153f3d0cbb5cc97) directly. Enable it with `substrate.enabled=true` in Obot's values.

It installs the API, controller, atelet DaemonSet, Envoy ingress/egress, pod certificate controller, PostgreSQL with persistent storage, gVisor SandboxConfig, and three CRDs. It uses upstream's fixed `ate-system` and `podcertificate-controller-system` namespaces and cluster-wide signer names. **Install only once per cluster**, into a cluster without an existing Substrate installation. Obot may run in a different namespace.

## Prerequisites

- Linux worker nodes and, when using bundled PostgreSQL, a default StorageClass (or set `substrate.postgres.storageClass`). PostgreSQL reserves 8Gi by default.
- Kubernetes with `ClusterTrustBundle`, `ClusterTrustBundleProjection`, and `PodCertificateRequest` enabled, and the `certificates.k8s.io/v1beta1` API served. These are cluster prerequisites; Helm cannot enable API-server or kubelet feature gates. See [upstream setup](https://github.com/agent-substrate/substrate/tree/v0.3.0/tools).
- Linux workers are selected automatically. Set `substrate.atelet.nodeSelector` to restrict both atelet and the POC WorkerPool to a node subset. atelet uses host paths and host ports 8085/9090; reserve these ports. The namespaces must permit these upstream pod specifications.
- An existing S3-compatible snapshot bucket, or GCS with application default credentials. Configure the same storage credentials for API and atelet through `substrate.storage.existingSecret` in `ate-system`, or pass `substrate.storage.credentials.AWS_ACCESS_KEY_ID` and `substrate.storage.credentials.AWS_SECRET_ACCESS_KEY` to have Helm create the Secret. Node workload identity can also supply credentials. Set the bucket URI in `substrate.poc.snapshotLocation` for the agent POC. This chart does not provision cloud resources.
- Images built from the pinned upstream commit. Upstream does not ship component images in its GitHub release.

## Build and install

From the Obot repository, publish to your registry (requires Go 1.27+, ko, make, curl, Docker buildx, and support for building linux/amd64 and linux/arm64):

```nu
bash tools/build-substrate-images.sh registry.example.com/substrate
```

Supply your normal Obot values plus:

```yaml
substrate:
  enabled: true
  image:
    repository: registry.example.com/substrate
  storage:
    backend: s3
    existingSecret: substrate-snapshot-credentials
    env:
      AWS_REGION: us-east-1
      # For a non-AWS S3 service:
      # AWS_ENDPOINT_URL: https://s3.example.com
      # AWS_S3_USE_PATH_STYLE: "true"
```

With `storage.existingSecret`, create the storage Secret in `ate-system` before installation, with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`. Alternatively, pass those keys under `substrate.storage.credentials`; Helm then creates the namespace and Secret in the same command. Do not set both options. If you precreate either Substrate namespace, add Helm's ownership metadata for the Obot release before installing. Registry pull Secrets must exist in both Substrate namespaces; reference them with `substrate.imagePullSecrets`. No public ingress is created for Substrate.

```nu
helm upgrade --install obot ./chart --namespace obot --create-namespace --values obot-values.yaml --wait --wait-for-jobs --timeout 10m
```

The bootstrap Job runs upstream `ate-setup create` commands to generate CA/JWT pools, derive the actor trust bundle, and discover the cluster's JWT issuer. Existing signing pools are preserved. `substrate.authentication.issuer` can override discovery. PostgreSQL uses upstream mutual TLS rather than a shared default password.

To use an external database, set `substrate.postgres.dsn` to its PostgreSQL connection string. A non-empty DSN skips the bundled database StatefulSet, Service, and ConfigMap and supplies the DSN to the API through a Kubernetes Secret. DSN changes restart the API pods. Leaving it empty retains the bundled database. Switching databases does not migrate existing data; use a separate database for Substrate rather than Obot's database.

The API is `api.ate-system.svc:443`; the actor router is `atenet-router.ate-system.svc`. By default only infrastructure is deployed. Set `substrate.poc.enabled=true` to add a shared Claude Code WorkerPool and enable Obot's instance controller and chat UI. See the [POC setup instructions](../../../runtime/claude-code/README.md) for the runtime image and snapshot configuration.

## Maintenance

Templates and CRDs are adapted from upstream `manifests/ate-install`. Changes are limited to image references, storage configuration, registry pull secrets, PostgreSQL sizing, retained namespaces, and Helm bootstrap/config resources. The chart retains upstream Apache-2.0 headers and LICENSE. Build and template sources must be upgraded together.

The pinned CRDs in `files/crds/` are rendered as retained Helm resources, so enabling Substrate on an existing release also installs them. A chart Job waits for CRDs to become established and applies the SandboxConfig and optional WorkerPool; no custom-resource discovery is needed while Helm validates the initial upgrade. A pre-delete Job removes those custom resources on uninstall. Review CRD schema changes before upgrading; Helm retains the CRDs on uninstall. Follow [upstream rolling-upgrade guidance](https://github.com/agent-substrate/substrate/blob/v0.3.0/docs/upgrade.md) before changing component versions or node selectors; draining actors is not handled by this chart.

Disabling Substrate or uninstalling Obot removes its running workloads. Namespaces, bootstrap-generated identity Secrets, PostgreSQL StatefulSet PVCs, and CRDs remain to avoid deleting durable state. Back up the database and signing pools together. Explicitly remove retained resources only when retiring the installation permanently.

Set `substrate.otel.endpoint` to an existing OTLP gRPC collector. Upstream always exports traces and metrics; leaving this unset uses localhost:4317 and produces exporter warnings without a local collector. Prometheus endpoints are also available.

Changing chart-managed storage credentials or storage environment values rolls the API and atelet pods. Changes to an existing Secret or telemetry environment values require restarting the affected workloads. For bootstrap failures, inspect `substrate-bootstrap-<revision>` in `ate-system`. If certificate-dependent pods remain Pending, inspect the certificate controller and cluster feature gates.
