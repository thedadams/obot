# 2026-10-02: Bundle upstream Agent Substrate with the Obot chart

- **Status:** Accepted
- **Date:** 2026-10-02
- **Supersedes:** None
- **Superseded by:** None

## Related issues

None.

## Related ODPs

None. This packages the runtime requested for the hosted-agent replacement; application integration is separate work.

## Context

Obot needs to deploy Agent Substrate alongside its server. The selected core upstream repository provides Kubernetes manifests and image builds but no Helm chart. A chart from a downstream fork would deploy different software.

## Decision

Bundle an opt-in local subchart derived from upstream v0.3.0, with upstream CRDs and a script to publish the matching images. Reuse upstream's identity bootstrap commands in a Kubernetes Job. Preserve upstream namespaces and certificate authentication. Schedule atelet and workers on Linux nodes by default, with a shared configurable node selector.

Render retained CRDs as Helm templates so an existing Obot release can enable Substrate in one upgrade. A Kubernetes Job applies custom resources after the CRDs are established; an uninstall hook removes those resources before their controllers. Accept inline snapshot credentials for a chart-managed Secret or an existing Secret.

## Rationale

This packages the requested source without introducing a fork of the runtime or implementing its key-generation formats in Obot.

## Consequences

The runtime is a cluster singleton and requires Kubernetes certificate features, eligible Linux nodes, published images, and snapshot storage. Obot maintains the Helm adaptations; CRD schema changes require review before chart upgrades. Durable state survives uninstall. Application integration is recorded separately in the agent POC decision.

## References

- [Pinned upstream source](https://github.com/agent-substrate/substrate/tree/ccecc788a327dc11dcd6c21ee153f3d0cbb5cc97)
- [Subchart operations](../chart/charts/substrate/README.md)
