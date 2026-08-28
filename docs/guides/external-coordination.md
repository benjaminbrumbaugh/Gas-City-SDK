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

## Delivery and response states

`accepted`, `queued`, and `running` describe transport and delivery progress.
`completed` means an authenticated external coordination response was recorded.
Transport acceptance is not execution completion.

```http
POST /v0/city/{city}/external-coordination/responses
X-GC-Request: external-coordination-adapter
X-GC-Coordination-Adapter: <adapter>
X-GC-Coordination-Adapter-Generation: <generation>
X-GC-Coordination-Adapter-Instance: <instance>
Content-Type: application/json
```

The response is correlated by the opaque `request_id` from the envelope; the API
also accepts the durable request record ID. The causal lifecycle may be retained
without retaining prompt or response content. External coordination is a
conversation boundary, not a durable information store.

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
