# External coordination

Gas City’s internal orchestrator can request help or decisions from an external
coordinator through a provider-neutral SDK contract. External coordination is
an optional capability; it does not own Gas City lifecycle or routine status.

## Discover the capability

```http
GET /v0/city/{city}/external-coordination
```

The response identifies the configured target, adapter, queued/non-interrupting
policy, session policy, eligible reasons, and declared adapter capabilities.
Declared capabilities are configuration evidence; live adapter registration is
a separate runtime condition.

## Register an adapter runtime

```http
POST /v0/city/{city}/extmsg/adapters
X-GC-Request: external-coordination-adapter-register
Content-Type: application/json

{
  "provider": "hermes",
  "account_id": "desktop",
  "name": "hermes",
  "callback_url": "http://127.0.0.1:8790"
}
```

The successful response returns a one-shot `credential`, `generation`, and
`instance`. Keep the credential in memory; adapter registration rejects an
`Idempotency-Key` so a generic response cache cannot replay the secret. Gas City
sends the credential as `Authorization: Bearer ...` on adapter callbacks. A
remote callback URL must use HTTPS; HTTP is accepted only for a literal loopback
host. User information, queries, and fragments are rejected before registration.

## Queue a request

```http
POST /v0/city/{city}/external-coordination/requests
X-GC-Request: gc-external-coordination
Content-Type: application/json
```

The request envelope carries the source agent, city/work/rig scope, reason,
prompt, correlation and idempotency keys, route identity, allowed tools, expiry,
and result destination. The defaults are `delivery_mode=queued` and
`session_mode=resume_or_create`.

The controller persists the request before attempting delivery when durable
retry, audit, or causal correlation is needed. Content retention is `ephemeral`
by default; in that mode prompt content is scrubbed after transport acceptance
or response and is not treated as a durable knowledge store. If the selected
`(provider, account_id)` transport is registered, the controller dispatches the
request through the adapter boundary. Otherwise the durable request remains
queued without blocking the orchestrator’s normal work.

The transport envelope forwards `content_retention`. An out-of-process adapter
must honor that policy for every copy it creates. If it cannot keep ephemeral
content out of its durable state, it must reject the delivery before durable
admission rather than silently treating the request as durable.

## Delivery and response states

`accepted`, `queued`, and `running` describe transport and delivery progress.
`completed` means an authenticated external coordination response was recorded.
Transport acceptance is not execution completion.

```http
POST /v0/city/{city}/external-coordination/responses
X-GC-Request: external-coordination-adapter
Authorization: Bearer <registration credential>
X-GC-Coordination-Adapter: <adapter>
X-GC-Coordination-Adapter-Generation: <generation>
X-GC-Coordination-Adapter-Instance: <instance>
Idempotency-Key: <response idempotency key>
Content-Type: application/json
```

The response is correlated by the opaque `request_id` from the envelope; the API
also accepts the durable request record ID. The causal lifecycle may be retained
without retaining prompt or response content. External coordination is a
conversation boundary, not a durable information store.

On graceful shutdown, unregister only the registration this process owns:

```http
DELETE /v0/city/{city}/extmsg/adapters
X-GC-Request: external-coordination-adapter-unregister
Content-Type: application/json

{
  "provider": "hermes",
  "account_id": "desktop",
  "generation": <generation>,
  "instance": "<instance>"
}
```

Missing fences are rejected and stale fences return a conflict without removing
the current replacement registration.

## CLI

Use `gc coordination show` to inspect configuration,
`gc coordination request` to queue work, and `gc coordination list` to inspect
durable request state. Routine patrol and health status stay in Gas City’s
normal beads, events, and dashboard surfaces.

## Hermes

Hermes is one possible adapter and target in `city.toml`; the generic SDK does
not hard-code Hermes session creation or credentials. A Hermes adapter registers
its authenticated runtime transport and owns provider-specific session
create/resume/prompt semantics.
