import { formatApiError } from './client';

export interface RoutingControlAvailability {
  available: boolean;
  reason?: string;
}

export interface RoutingStatus {
  available: boolean;
  reason?: string;
  collector?: {
    healthy: boolean;
    lastRunAt: string | null;
    nextRunAt: string | null;
    lastError?: string | null;
  };
  controls?: {
    collect?: RoutingControlAvailability;
    resolve?: RoutingControlAvailability;
  };
  retention?: {
    healthy: boolean;
    retentionDays: number;
    lastSweepAt: string | null;
    retainedDecisions: number;
    oldestDecisionAt?: string | null;
    reason?: string;
  };
}

export interface RoutingTarget {
  id: string;
  provider: string;
  model: string;
  source: string;
  account: string;
  capabilities: string[];
  configDigest: string;
  enabled: boolean;
}

export interface EligibleRoutingWork {
  workId: string;
  title: string;
  rig: string;
  revision: string;
  observedAt: string;
}

export interface RoutingDecisionTarget {
  id: string;
  provider: string;
  model: string;
  source: string;
  account: string;
}

export interface RoutingDecision {
  id: string;
  workId: string;
  lifecycle: string;
  reason: string;
  createdAt: string;
  expiresAt: string | null;
  target: RoutingDecisionTarget | null;
}

export interface RoutingUsage {
  provider: string;
  model: string;
  source: string;
  account: string;
  decisions: number;
  inputTokens?: number;
  outputTokens?: number;
}

export interface RoutingList<T> {
  available: boolean;
  reason?: string;
  sampledAt?: string;
  items: T[];
}

export interface RoutingDecisions extends RoutingList<RoutingDecision> {
  lifecycleCounts: Record<string, number>;
}

export interface RoutingActionResult {
  ok: boolean;
  message: string;
}

type Decoder<T> = (value: unknown, url: string) => T;

async function routingRequest<T>(
  method: 'GET' | 'POST',
  path: string,
  decode: Decoder<T>,
  body?: object,
) {
  const url = `/api/routing/${path}`;
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (method === 'POST') headers['X-GC-Request'] = 'dashboard';
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const response = await fetch(url, {
    method,
    headers,
    credentials: 'same-origin',
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  if (!response.ok) {
    let message = response.statusText || `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: unknown };
      if (typeof payload.error === 'string') message = payload.error;
    } catch {
      // Preserve the HTTP status text when an unavailable backend returns non-JSON.
    }
    throw new Error(`${response.status} ${message}`);
  }
  const value: unknown = await response.json();
  return decode(value, url);
}

function record(value: unknown, url: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`Invalid API response for ${url}: expected an object`);
  }
  return value as Record<string, unknown>;
}

function passthrough<T>(value: unknown, url: string): T {
  return record(value, url) as T;
}

function list<T>(value: unknown, url: string): RoutingList<T> {
  const result = record(value, url);
  if (typeof result.available !== 'boolean')
    throw new Error(`Invalid API response for ${url}: available must be boolean`);
  if (!Array.isArray(result.items))
    throw new Error(`Invalid API response for ${url}: items must be an array`);
  return result as unknown as RoutingList<T>;
}

export const routingApi = {
  status: () => routingRequest('GET', 'status', passthrough<RoutingStatus>),
  targets: () => routingRequest('GET', 'targets', list<RoutingTarget>),
  eligible: () => routingRequest('GET', 'eligible', list<EligibleRoutingWork>),
  decisions: () => routingRequest('GET', 'decisions', passthrough<RoutingDecisions>),
  usage: () => routingRequest('GET', 'usage', list<RoutingUsage>),
  collect: () => routingRequest('POST', 'collect', passthrough<RoutingActionResult>),
  resolve: (workId: string) =>
    routingRequest('POST', 'resolve', passthrough<RoutingActionResult>, { workId }),
};

export function routingError(error: unknown): string {
  return error instanceof Error ? error.message : formatApiError(error, 'routing request failed');
}
