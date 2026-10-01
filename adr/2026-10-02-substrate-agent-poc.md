# 2026-10-02: Claude Code agent POC on Substrate

- **Status:** Accepted
- **Date:** 2026-10-02
- **Supersedes:** None
- **Superseded by:** None

## Related issues

None.

## Related ODPs

None. This is an explicitly scoped proof of concept.

## Context

Hosted Agents has been removed. We need to validate interactive Claude Code
sessions on the bundled upstream Substrate before designing its replacement.

## Decision

Store owner-scoped AgentInstances in Obot and reconcile them through an Obot
controller using Substrate's native control API. Use one shared Kubernetes
worker namespace, one atespace per owner, and a private actor/template pair per
instance. Proxy chat through Obot and use an instance-specific scoped Obot API
key for model and MCP access. Snapshot workspace and session files as DATA and
cold-boot the runtime on resume.

## Rationale

This keeps ownership and authorization in Obot, avoids a new Kubernetes CRD
bridge, and exercises Substrate's existing lifecycle. Private templates avoid
sharing credentials through golden snapshots. Data snapshots preserve the
conversation without restoring stale HTTP streams.

## Consequences

The POC supports one conversation and one active turn per instance, with manual
suspension and no sharing or recovery UI. Credentials are in private Substrate
templates and snapshot data as well as Obot's credential store. The control
plane and snapshot storage therefore remain trusted infrastructure. Deletion
revokes the key before removing the actor. This is not a production API contract.

## References

- [Setup and scope](../runtime/claude-code/README.md)
- [Bundled upstream chart](2026-10-02-bundle-agent-substrate.md)
