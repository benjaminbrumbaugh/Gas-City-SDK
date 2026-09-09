# Convoy lifecycle callback contract v1

Status: Accepted. The implementing package `internal/convoycallback` is not
on `main` yet; it lands with the harness-callbacks delivery candidate.

This is the Gas City-owned, harness-neutral record exchanged for lifecycle
notifications about a launched convoy. It is a schema contract, not a
transport or delivery policy.

## Version and lifecycle kinds

Every new callback record carries:

```json
{
  "schema_version": "convoy-callback.v1",
  "type": "convoy.created"
}
```

The supported kinds are:

| Kind | Required scoped identity | Meaning |
| --- | --- | --- |
| `convoy.ready_for_rebuild` | — | Work is ready for a rebuild action. It does not create, accept, close, or deploy anything. |
| `convoy.created` | — | The convoy container was created. |
| `task.accepted` | `task_id` | One task in the convoy was accepted. |
| `convoy.closed` | — | The convoy container was closed. Closure does not imply deployment. |
| `deployment.completed` | `deployment_id` | An associated deployment completed. Deployment is a separate lifecycle phase. |

All records require `event_id`, `convoy_id`, `launch_origin`,
`correlation_id`, and `occurred_at`. `launch_origin` is captured by the
ordinary sling path in the producer implementation; this package validates
only that it is present, bounded, UTF-8, and free of control characters. It
does not parse or interpret the value.

`route_identity` is optional opaque key/value route data. It is bounded for
durable-record safety, but Gas City does not infer a provider, harness, role,
conversation, URL, credential, or private runtime from it. Callback URLs and
credentials are not fields in this contract.

## Compatibility

`convoy.created` and `convoy.closed` events written before this contract had
no schema version, event ID, correlation ID, or launch origin. A consumer that
needs to read those records may pass the old type, subject, and timestamp to
`DecodeLegacyEvent`. The result is explicitly marked `legacy.convoy.v0` and
is a read-only projection; it cannot be passed as a v1 callback record.

New callback records must use `convoy-callback.v1`. A v1 validator rejects
unknown versions, unknown lifecycle kinds, missing common identity, malformed
opaque values, and missing phase-specific identity. JSON decoding remains
forward-compatible with unknown optional fields; that compatibility does not
weaken required-field or version validation.

## Ownership and evidence boundary

This contract does not prove that a launch origin is truthful, a recipient is
authorized, a lifecycle event was emitted, a callback was delivered, a rebuild
succeeded, or a deployment completed. Those behaviors belong to sling capture,
subscription authorization, lifecycle fan-out, transport, rebuild, and
deployment owners respectively.
