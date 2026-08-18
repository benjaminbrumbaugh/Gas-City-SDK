import { useCallback, useEffect, useState, type ReactNode } from 'react';
import {
  routingApi,
  routingError,
  type EligibleRoutingWork,
  type RoutingDecisions,
  type RoutingList,
  type RoutingStatus,
  type RoutingTarget,
  type RoutingUsage,
} from '../api/routing';
import { Button } from '../components/Button';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/StatusBadge';

interface Source<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

type SourceKey = 'status' | 'targets' | 'eligible' | 'decisions' | 'usage';

export function RoutingPage() {
  const [status, setStatus] = useState<Source<RoutingStatus>>(emptySource());
  const [targets, setTargets] = useState<Source<RoutingList<RoutingTarget>>>(emptySource());
  const [eligible, setEligible] = useState<Source<RoutingList<EligibleRoutingWork>>>(emptySource());
  const [decisions, setDecisions] = useState<Source<RoutingDecisions>>(emptySource());
  const [usage, setUsage] = useState<Source<RoutingList<RoutingUsage>>>(emptySource());
  const [selectedWork, setSelectedWork] = useState('');
  const [action, setAction] = useState<{ kind: 'success' | 'error'; message: string } | null>(null);
  const [actionPending, setActionPending] = useState(false);

  const refresh = useCallback(async () => {
    const loads = [
      loadSource('status', routingApi.status, setStatus),
      loadSource('targets', routingApi.targets, setTargets),
      loadSource('eligible', routingApi.eligible, setEligible),
      loadSource('decisions', routingApi.decisions, setDecisions),
      loadSource('usage', routingApi.usage, setUsage),
    ];
    await Promise.all(loads);
  }, []);

  useEffect(() => void refresh(), [refresh]);
  useEffect(() => {
    const rows = eligible.data?.items ?? [];
    if (rows.length === 0) setSelectedWork('');
    else if (!rows.some((row) => row.workId === selectedWork)) setSelectedWork(rows[0]!.workId);
  }, [eligible.data, selectedWork]);

  const runAction = async (kind: 'collect' | 'resolve') => {
    setAction(null);
    setActionPending(true);
    try {
      const result =
        kind === 'collect' ? await routingApi.collect() : await routingApi.resolve(selectedWork);
      if (!result.ok) throw new Error(result.message || `${kind} failed`);
      setAction({ kind: 'success', message: result.message || `${kind} completed` });
      await refresh();
    } catch (error) {
      setAction({ kind: 'error', message: routingError(error) });
    } finally {
      setActionPending(false);
    }
  };

  const collectControl = status.data?.controls?.collect;
  const resolveControl = status.data?.controls?.resolve;
  const collectUnavailable =
    status.data !== null && (!status.data.available || collectControl?.available === false);
  const resolveUnavailable =
    status.data !== null && (!status.data.available || resolveControl?.available === false);
  const initialLoading = status.loading && status.data === null;

  return (
    <section>
      <PageHeader
        title="Routing"
        synopsis="Deterministic target collection, eligibility, decisions, usage, and retention."
        meta={
          <div className="flex flex-wrap items-center gap-3">
            <Button onClick={() => void refresh()}>Refresh</Button>
            <Button
              onClick={() => void runAction('collect')}
              disabled={actionPending || collectUnavailable || status.data === null}
            >
              Run collection now
            </Button>
            <Button
              onClick={() => void runAction('resolve')}
              disabled={actionPending || resolveUnavailable || selectedWork === ''}
            >
              Resolve preview
            </Button>
          </div>
        }
      />

      {action && (
        <p
          role={action.kind === 'success' ? 'status' : 'alert'}
          className={`mb-8 text-body ${action.kind === 'success' ? 'text-ok' : 'text-accent'}`}
        >
          {action.message}
        </p>
      )}
      {collectControl?.available === false && <UnavailableControl reason={collectControl.reason} />}
      {resolveControl?.available === false && <UnavailableControl reason={resolveControl.reason} />}

      {initialLoading && <p className="text-body text-fg-muted italic">Loading routing state.</p>}

      <div className="space-y-12">
        <Panel title="Collector">
          <SourceError name="status" source={status} />
          {status.data && !status.data.available ? (
            <Unavailable reason={status.data.reason} />
          ) : status.data?.collector ? (
            <DefinitionList>
              <Datum
                label="Health"
                value={status.data.collector.healthy ? 'healthy' : 'degraded'}
              />
              <Datum label="Last run" value={date(status.data.collector.lastRunAt)} />
              <Datum label="Next run" value={date(status.data.collector.nextRunAt)} />
              {status.data.collector.lastError && (
                <Datum label="Last error" value={status.data.collector.lastError} warn />
              )}
            </DefinitionList>
          ) : null}
        </Panel>

        <Panel title="Static targets" count={targets.data?.items.length}>
          <SourceError name="targets" source={targets} />
          {targets.data && !targets.data.available ? (
            <Unavailable reason={targets.data.reason} />
          ) : targets.data?.items.length === 0 ? (
            <Empty>No static routing targets configured.</Empty>
          ) : targets.data ? (
            <TargetsTable rows={targets.data.items} />
          ) : null}
        </Panel>

        <Panel title="Fresh eligible work" count={eligible.data?.items.length}>
          <SourceError name="eligible work" source={eligible} />
          {eligible.data && !eligible.data.available ? (
            <Unavailable reason={eligible.data.reason} />
          ) : eligible.data?.items.length === 0 ? (
            <Empty>No fresh eligible work.</Empty>
          ) : eligible.data ? (
            <div className="space-y-4">
              <label className="text-body text-fg-muted">
                Preview work{' '}
                <select
                  aria-label="Eligible work"
                  value={selectedWork}
                  onChange={(event) => setSelectedWork(event.target.value)}
                  className="ml-2 border border-rule rounded-sm bg-surface px-2 py-1 text-fg focus-mark"
                >
                  {eligible.data.items.map((row) => (
                    <option key={row.workId} value={row.workId}>
                      {row.workId} · {row.title}
                    </option>
                  ))}
                </select>
              </label>
              <EligibleTable rows={eligible.data.items} />
            </div>
          ) : null}
        </Panel>

        <Panel title="Persisted decisions" count={decisions.data?.items.length}>
          <SourceError name="decisions" source={decisions} />
          {decisions.data && !decisions.data.available ? (
            <Unavailable reason={decisions.data.reason} />
          ) : decisions.data ? (
            <div className="space-y-7">
              <LifecycleCounts counts={decisions.data.lifecycleCounts} />
              {decisions.data.items.length === 0 ? (
                <Empty>No persisted routing decisions.</Empty>
              ) : (
                <DecisionsTable rows={decisions.data.items} />
              )}
            </div>
          ) : null}
        </Panel>

        <Panel title="Usage by target dimensions" count={usage.data?.items.length}>
          <SourceError name="usage" source={usage} />
          {usage.data && !usage.data.available ? (
            <Unavailable reason={usage.data.reason} />
          ) : usage.data?.items.length === 0 ? (
            <Empty>No routing usage recorded.</Empty>
          ) : usage.data ? (
            <UsageTable rows={usage.data.items} />
          ) : null}
        </Panel>

        <Panel title="Retention">
          {status.data?.retention ? (
            <DefinitionList>
              <Datum
                label="Status"
                value={status.data.retention.healthy ? 'healthy' : 'degraded'}
              />
              <Datum label="Policy" value={`${status.data.retention.retentionDays} days`} />
              <Datum
                label="Retained decisions"
                value={number(status.data.retention.retainedDecisions)}
              />
              <Datum label="Last sweep" value={date(status.data.retention.lastSweepAt)} />
              <Datum
                label="Oldest decision"
                value={date(status.data.retention.oldestDecisionAt ?? null)}
              />
              {status.data.retention.reason && (
                <Datum label="Detail" value={status.data.retention.reason} warn />
              )}
            </DefinitionList>
          ) : status.data ? (
            <Unavailable reason="retention status not reported" />
          ) : null}
        </Panel>
      </div>
    </section>
  );
}

function emptySource<T>(): Source<T> {
  return { data: null, error: null, loading: true };
}

async function loadSource<T>(
  _key: SourceKey,
  fetcher: () => Promise<T>,
  setter: (value: Source<T> | ((old: Source<T>) => Source<T>)) => void,
) {
  setter((old) => ({ ...old, loading: true, error: null }));
  try {
    setter({ data: await fetcher(), error: null, loading: false });
  } catch (error) {
    setter((old) => ({ ...old, error: routingError(error), loading: false }));
  }
}

function Panel({
  title,
  count,
  children,
}: {
  title: string;
  count?: number | undefined;
  children: ReactNode;
}) {
  return (
    <section>
      <header className="flex items-baseline justify-between gap-4 mb-4 pb-2 border-b border-rule">
        <h2 className="text-headline font-semibold text-fg">{title}</h2>
        {count !== undefined && (
          <span className="text-label uppercase tracking-wider text-fg-muted">{count} rows</span>
        )}
      </header>
      {children}
    </section>
  );
}

function SourceError<T>({ name, source }: { name: string; source: Source<T> }) {
  return source.error ? (
    <p role="alert" className="text-body text-accent">
      {name} unavailable: {source.error}
    </p>
  ) : null;
}
function Unavailable({ reason }: { reason?: string | undefined }) {
  return (
    <p className="text-body text-fg-muted italic">Unavailable{reason ? `: ${reason}` : ''}.</p>
  );
}
function UnavailableControl({ reason }: { reason?: string | undefined }) {
  return reason ? <p className="mb-2 text-label text-warn">{reason}</p> : null;
}
function Empty({ children }: { children: ReactNode }) {
  return <p className="text-body text-fg-muted italic">{children}</p>;
}
function DefinitionList({ children }: { children: ReactNode }) {
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-8 gap-y-3 max-w-prose">{children}</dl>
  );
}
function Datum({ label, value, warn = false }: { label: string; value: string; warn?: boolean }) {
  return (
    <>
      <dt className="text-body text-fg-muted">{label}</dt>
      <dd className={`text-body tnum font-medium ${warn ? 'text-warn' : 'text-fg'}`}>{value}</dd>
    </>
  );
}
function date(value: string | null) {
  return value ? new Date(value).toLocaleString() : 'not reported';
}
function number(value: number | undefined) {
  return value === undefined ? 'not reported' : value.toLocaleString();
}

const th = 'pb-2 pr-5 text-left text-label uppercase tracking-wider text-fg-muted font-medium';
const td = 'py-3 pr-5 align-top text-body text-fg border-t border-rule';
function Table({ headers, children }: { headers: string[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full">
        <thead>
          <tr>
            {headers.map((h) => (
              <th key={h} className={th}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

function TargetsTable({ rows }: { rows: RoutingTarget[] }) {
  return (
    <Table
      headers={[
        'Target',
        'Provider',
        'Model',
        'Source',
        'Account',
        'Capabilities',
        'Config digest',
        'State',
      ]}
    >
      {rows.map((r) => (
        <tr key={r.id}>
          <td className={td}>{r.id}</td>
          <td className={td}>{r.provider}</td>
          <td className={td}>{r.model}</td>
          <td className={td}>{r.source}</td>
          <td className={td}>{r.account}</td>
          <td className={td}>{r.capabilities.join(', ') || 'none'}</td>
          <td className={`${td} font-mono text-label`}>{r.configDigest}</td>
          <td className={td}>
            <StatusBadge
              tone={r.enabled ? 'ok' : 'warn'}
              label={r.enabled ? 'enabled' : 'disabled'}
            />
          </td>
        </tr>
      ))}
    </Table>
  );
}
function EligibleTable({ rows }: { rows: EligibleRoutingWork[] }) {
  return (
    <Table headers={['Work', 'Title', 'Rig', 'Revision', 'Observed']}>
      {rows.map((r) => (
        <tr key={r.workId}>
          <td className={td}>{r.workId}</td>
          <td className={td}>{r.title}</td>
          <td className={td}>{r.rig}</td>
          <td className={td}>{r.revision}</td>
          <td className={td}>{date(r.observedAt)}</td>
        </tr>
      ))}
    </Table>
  );
}
function LifecycleCounts({ counts }: { counts: Record<string, number> }) {
  const rows = Object.entries(counts);
  return rows.length === 0 ? null : (
    <div className="flex flex-wrap gap-x-8 gap-y-3">
      {rows.map(([label, value]) => (
        <div key={label}>
          <div className="text-label uppercase tracking-wider text-fg-muted">{label}</div>
          <div className="text-title font-semibold tnum text-fg">{number(value)}</div>
        </div>
      ))}
    </div>
  );
}
function DecisionsTable({ rows }: { rows: RoutingDecisions['items'] }) {
  return (
    <Table headers={['Work', 'Lifecycle', 'Target', 'Reason', 'Created', 'Expires']}>
      {rows.map((r) => (
        <tr key={r.id}>
          <td className={td}>{r.workId}</td>
          <td className={td}>{r.lifecycle}</td>
          <td className={td}>
            {r.target
              ? `${r.target.provider}/${r.target.model} · ${r.target.source}/${r.target.account}`
              : 'none'}
          </td>
          <td className={`${td} max-w-md`}>{r.reason}</td>
          <td className={td}>{date(r.createdAt)}</td>
          <td className={td}>{date(r.expiresAt)}</td>
        </tr>
      ))}
    </Table>
  );
}
function UsageTable({ rows }: { rows: RoutingUsage[] }) {
  return (
    <Table
      headers={[
        'Provider',
        'Model',
        'Source',
        'Account',
        'Decisions',
        'Input tokens',
        'Output tokens',
      ]}
    >
      {rows.map((r, index) => (
        <tr key={`${r.provider}:${r.model}:${r.source}:${r.account}:${index}`}>
          <td className={td}>{r.provider}</td>
          <td className={td}>{r.model}</td>
          <td className={td}>{r.source}</td>
          <td className={td}>{r.account}</td>
          <td className={td}>{number(r.decisions)}</td>
          <td className={td}>{number(r.inputTokens)}</td>
          <td className={td}>{number(r.outputTokens)}</td>
        </tr>
      ))}
    </Table>
  );
}
