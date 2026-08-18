import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RoutingPage } from './Routing';

const data = {
  status: {
    available: true,
    collector: {
      healthy: true,
      lastRunAt: '2026-08-07T12:00:00Z',
      nextRunAt: '2026-08-07T12:05:00Z',
      lastError: null,
    },
    controls: {
      collect: { available: true },
      resolve: { available: true },
    },
    retention: {
      healthy: true,
      retentionDays: 30,
      lastSweepAt: '2026-08-07T11:00:00Z',
      retainedDecisions: 42,
      oldestDecisionAt: '2026-07-10T09:00:00Z',
    },
  },
  targets: {
    available: true,
    items: [
      {
        id: 'anthropic-primary',
        provider: 'anthropic',
        model: 'claude-opus-4-1',
        source: 'static',
        account: 'city-prod',
        capabilities: ['tools', 'vision'],
        configDigest: 'sha256:abc123',
        enabled: true,
      },
    ],
  },
  eligible: {
    available: true,
    sampledAt: '2026-08-07T12:01:00Z',
    items: [
      {
        workId: 'gc-abc',
        title: 'Route this work',
        rig: 'sdk',
        revision: '7',
        observedAt: '2026-08-07T12:00:30Z',
      },
    ],
  },
  decisions: {
    available: true,
    lifecycleCounts: { proposed: 2, approved: 3, admitted: 8, refused: 1, expired: 4 },
    items: [
      {
        id: 'decision-1',
        workId: 'gc-abc',
        lifecycle: 'approved',
        reason: 'capability match and available account',
        createdAt: '2026-08-07T12:00:40Z',
        expiresAt: '2026-08-07T12:10:40Z',
        target: {
          id: 'anthropic-primary',
          provider: 'anthropic',
          model: 'claude-opus-4-1',
          source: 'static',
          account: 'city-prod',
        },
      },
    ],
  },
  usage: {
    available: true,
    items: [
      {
        provider: 'anthropic',
        model: 'claude-opus-4-1',
        source: 'static',
        account: 'city-prod',
        decisions: 9,
        inputTokens: 1200,
        outputTokens: 340,
      },
    ],
  },
};

let responses: Record<string, unknown>;
let pendingStatus: Promise<Response> | null;

beforeEach(() => {
  responses = structuredClone(data) as unknown as Record<string, unknown>;
  pendingStatus = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/routing/status' && pendingStatus !== null) return pendingStatus;
      if (init?.method === 'POST') {
        if (url === '/api/routing/collect')
          return json({ ok: true, message: 'collection completed' });
        if (url === '/api/routing/resolve') return json({ ok: true, message: 'preview resolved' });
      }
      const key = url.replace('/api/routing/', '');
      if (!(key in responses)) return json({ error: 'not found' }, 404);
      const value = responses[key];
      if (value instanceof Error) return json({ error: value.message }, 503);
      return json(value);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('RoutingPage', () => {
  it('shows a loading state while the routing snapshot is pending', () => {
    pendingStatus = new Promise(() => undefined);
    render(<RoutingPage />);
    expect(screen.getByText('Loading routing state.')).toBeTruthy();
  });

  it('surfaces endpoint failures without hiding successful sections', async () => {
    responses.targets = new Error('targets offline');
    render(<RoutingPage />);
    expect(await screen.findByText(/targets unavailable: 503 targets offline/i)).toBeTruthy();
    expect((await screen.findAllByText('gc-abc')).length).toBeGreaterThan(0);
  });

  it('renders explicit empty states and disables resolve without eligible work', async () => {
    responses.targets = { available: true, items: [] };
    responses.eligible = { available: true, items: [] };
    responses.decisions = { available: true, lifecycleCounts: {}, items: [] };
    responses.usage = { available: true, items: [] };
    render(<RoutingPage />);
    expect(await screen.findByText('No static routing targets configured.')).toBeTruthy();
    expect(screen.getByText('No fresh eligible work.')).toBeTruthy();
    expect(screen.getByText('No persisted routing decisions.')).toBeTruthy();
    expect(screen.getByText('No routing usage recorded.')).toBeTruthy();
    expect(
      (screen.getByRole('button', { name: /resolve preview/i }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it('renders collector, targets, eligible work, lifecycle, decisions, usage, and retention data', async () => {
    render(<RoutingPage />);
    expect((await screen.findAllByText('healthy')).length).toBe(2);
    expect(screen.getByText('anthropic-primary')).toBeTruthy();
    expect(screen.getByText('sha256:abc123')).toBeTruthy();
    expect(screen.getByText('tools, vision')).toBeTruthy();
    expect(screen.getAllByText('gc-abc')).toHaveLength(2);
    expect(screen.getByText('admitted')).toBeTruthy();
    expect(screen.getByText('8')).toBeTruthy();
    expect(screen.getByText('capability match and available account')).toBeTruthy();
    expect(screen.getByText('1,200')).toBeTruthy();
    expect(screen.getByText('30 days')).toBeTruthy();
    expect(screen.getByText('42')).toBeTruthy();
  });

  it('runs collection, refreshes all reads, and reports success', async () => {
    render(<RoutingPage />);
    await screen.findByText('anthropic-primary');
    const before = (fetch as ReturnType<typeof vi.fn>).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: /run collection now/i }));
    expect((await screen.findByRole('status')).textContent).toContain('collection completed');
    expect(fetch).toHaveBeenCalledWith(
      '/api/routing/collect',
      expect.objectContaining({ method: 'POST' }),
    );
    expect((fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(before + 1);
  });

  it('resolves a preview for selected eligible work and reports failures', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === '/api/routing/resolve' && init?.method === 'POST') {
          expect(JSON.parse(String(init.body))).toEqual({ workId: 'gc-abc' });
          return json({ error: 'resolver busy' }, 409);
        }
        const key = url.replace('/api/routing/', '');
        return json(responses[key]);
      },
    );
    render(<RoutingPage />);
    await screen.findByRole('combobox', { name: /eligible work/i });
    fireEvent.click(screen.getByRole('button', { name: /resolve preview/i }));
    expect((await screen.findByRole('alert')).textContent).toContain('409 resolver busy');
  });

  it('disables unavailable controls and explains why', async () => {
    responses.status = {
      ...data.status,
      controls: {
        collect: { available: false, reason: 'collector not configured' },
        resolve: { available: false, reason: 'resolver disabled' },
      },
    };
    render(<RoutingPage />);
    await screen.findByText('collector not configured');
    expect(
      (screen.getByRole('button', { name: /run collection now/i }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(
      (screen.getByRole('button', { name: /resolve preview/i }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(screen.getByText('resolver disabled')).toBeTruthy();
  });

  it('refreshes every routing read on demand', async () => {
    render(<RoutingPage />);
    await screen.findByText('anthropic-primary');
    const before = (fetch as ReturnType<typeof vi.fn>).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: /^refresh$/i }));
    await waitFor(() =>
      expect((fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBe(before + 5),
    );
  });
});

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
