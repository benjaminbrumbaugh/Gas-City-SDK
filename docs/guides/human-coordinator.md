# Human Coordinator Agent affordance

Gas City’s internal orchestrator can contact an external Human Coordinator Agent
(HCA) through a provider-neutral SDK contract. The HCA is not the Mayor and does
not own Gas City lifecycle.

## Discover the signifier

```http
GET /v0/city/{city}/hca
```

The response identifies the logical target, adapter, queued/non-interrupting
policy, session policy, eligible reasons, and declared adapter capabilities.
Declared capabilities are configuration evidence; live adapter registration is a
separate runtime condition.

## Queue a request

```http
POST /v0/city/{city}/hca/requests
X-GC-Request: gc-hca
Content-Type: application/json
```

The request envelope carries the source agent, city/work/rig scope, reason,
prompt, correlation and idempotency keys, route identity, allowed tools, expiry,
and result destination. The default is `delivery_mode=queued` and
`session_mode=resume_or_create`.

The controller persists the request envelope before attempting delivery when
durable retry, audit, or causal correlation is needed. Content retention is
`ephemeral` by default; in that mode the prompt is scrubbed after transport acceptance
or response and is not treated as a durable knowledge store. If the selected
`(provider, account_id)` transport is registered, the controller dispatches it
through the adapter boundary. If it is not registered, a durable request
remains queued; an HCA absence must not block the Mayor's normal mailbox,
self-tasking, schedules, patrol, or recovery mechanisms.

## Delivery and response states

`accepted`/`queued`/`running` describe transport and delivery progress.
`completed` means an authenticated HCA response was recorded. Transport
acceptance is not HCA execution completion.

```http
POST /v0/city/{city}/hca/responses
X-GC-Request: hca-adapter
Content-Type: application/json
```

The response is correlated by the opaque `request_id` from the envelope (the
SDK also accepts the durable request record ID at the API boundary). The causal request lifecycle may be retained without retaining the prompt or
response content. The HCA is an optional external conversation surface, not a
durable information store.

## Mayor signifier

The Mayor prompt receives a dedicated `human-coordinator` fragment. It directs
the Mayor to use `gc hca show` before calling and `gc hca request` for outside
help, escalation, authorization, direct-request responses, follow-up questions,
and large decision-relevant summaries. Routine patrol and health status stay in
Gas City’s normal beads, events, and dashboard surfaces.

## Hermes

Hermes is configured as one adapter/target in `city.toml`; the generic SDK does
not hard-code Hermes session creation or credentials. A Hermes adapter registers
its authenticated runtime transport and owns provider-specific session
create/resume/prompt semantics.
