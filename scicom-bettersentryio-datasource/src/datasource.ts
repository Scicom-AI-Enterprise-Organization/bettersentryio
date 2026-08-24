import {
  createDataFrame,
  DataFrame,
  DataQueryRequest,
  DataQueryResponse,
  DataSourceApi,
  DataSourceInstanceSettings,
  FieldType,
} from '@grafana/data';
import { getBackendSrv, getTemplateSrv } from '@grafana/runtime';
import { lastValueFrom } from 'rxjs';

import { BsioDataSourceOptions, BsioQuery, DEFAULT_QUERY } from './types';

/**
 * A frontend datasource: queries run in the browser and reach the engine through
 * Grafana's data proxy (the `engine` route in plugin.json), which attaches the API
 * token server-side. No backend binary to build or sign; the cost is that
 * Grafana-managed alert rules cannot use it — which costs nothing here, because
 * bettersentryio is itself the alerter.
 */
export class BsioDataSource extends DataSourceApi<BsioQuery, BsioDataSourceOptions> {
  private readonly proxy: string;

  constructor(instanceSettings: DataSourceInstanceSettings<BsioDataSourceOptions>) {
    super(instanceSettings);
    // instanceSettings.url is the per-datasource proxy base; `/engine` selects the
    // route that knows the engine's address and holds the token.
    this.proxy = `${instanceSettings.url}/engine`;
  }

  getDefaultQuery(): Partial<BsioQuery> {
    return DEFAULT_QUERY;
  }

  private async get<T>(path: string, params: Record<string, unknown>): Promise<T> {
    const response = await lastValueFrom(
      getBackendSrv().fetch<T>({
        url: `${this.proxy}${path}`,
        method: 'GET',
        params,
        showErrorAlert: false,
      })
    );
    return response.data;
  }

  /** The app dropdown in the query editor. */
  async listApps(): Promise<Array<{ slug: string; name: string }>> {
    const data = await this.get<{ apps: Array<{ slug: string; name: string }> }>('/api/0/apps', {});
    return data.apps;
  }

  async query(request: DataQueryRequest<BsioQuery>): Promise<DataQueryResponse> {
    const { range, targets, scopedVars } = request;
    const tmpl = getTemplateSrv();
    const interp = (v?: string) => (v ? tmpl.replace(v, scopedVars) : v);

    // The engine takes absolute instants; Grafana's range is already exactly that.
    const window = { start: range.from.toISOString(), end: range.to.toISOString() };
    // Explicit interval from the panel, in whole seconds. The engine honours an
    // explicit value verbatim and only auto-snaps when none is sent.
    const interval = `${Math.max(1, Math.round(request.intervalMs / 1000))}s`;

    const frames = await Promise.all(
      targets
        .filter((t) => !t.hide)
        .map((t) => this.runQuery({ ...DEFAULT_QUERY, ...t, app: interp(t.app) }, window, interval, interp))
    );
    return { data: frames.flat() };
  }

  private async runQuery(
    q: BsioQuery,
    window: { start: string; end: string },
    interval: string,
    interp: (v?: string) => string | undefined
  ): Promise<DataFrame[]> {
    switch (q.kind) {
      case 'events': {
        if (!q.app) {
          return [];
        }
        const data = await this.get<{
          levels: string[];
          buckets: Array<{ at: string; counts: Record<string, number> }>;
        }>(`/api/0/apps/${encodeURIComponent(q.app)}/series`, { ...window, interval });
        const time = data.buckets.map((b) => Date.parse(b.at));
        // One number field per level: typed numbers, zero-filled buckets — the two
        // things the Sentry datasource made the dashboard fix by hand.
        const levels = data.levels.length > 0 ? data.levels : ['events'];
        return [
          createDataFrame({
            refId: q.refId,
            fields: [
              { name: 'time', type: FieldType.time, values: time },
              ...levels.map((level) => ({
                name: level,
                type: FieldType.number,
                values: data.buckets.map((b) => b.counts[level] ?? 0),
              })),
            ],
          }),
        ];
      }

      case 'issues': {
        if (!q.app) {
          return [];
        }
        const params: Record<string, unknown> = { project: q.app, ...window };
        if (q.includeResolved) {
          params.resolved = 'true';
          params.archived = 'true';
        }
        if (q.limit) {
          params.limit = q.limit;
        }
        const data = await this.get<{ issues: Issue[] }>('/api/0/issues', params);
        return [issueFrame(q.refId, data.issues)];
      }

      case 'monitors': {
        // /api/0/overview, not /api/0/monitors: the wall endpoint is the lean
        // shell-loop shape (keyed "monitor", no app or uptime); overview carries
        // the full DTO the operator UI renders.
        const data = await this.get<{ monitors: Monitor[] }>('/api/0/overview', {});
        return [
          tableFrame(q.refId, data.monitors, [
            { name: 'monitor', type: FieldType.string, of: (m: Monitor) => m.slug },
            { name: 'app', type: FieldType.string, of: (m) => m.app },
            { name: 'status', type: FieldType.string, of: (m) => m.status },
            { name: 'environment', type: FieldType.string, of: (m) => m.environment },
            { name: 'last beat', type: FieldType.time, of: (m) => parseTime(m.last_beat_at) },
            { name: 'uptime %', type: FieldType.number, of: (m) => m.uptime_pct },
            { name: 'beats 24h', type: FieldType.number, of: (m) => m.beats_24h },
          ]),
        ];
      }

      case 'incidents': {
        const data = await this.get<{ incidents: Incident[] }>('/api/0/incidents', {});
        return [
          tableFrame(q.refId, data.incidents, [
            { name: 'monitor', type: FieldType.string, of: (i: Incident) => i.monitor },
            { name: 'kind', type: FieldType.string, of: (i) => i.kind },
            { name: 'opened', type: FieldType.time, of: (i) => parseTime(i.opened_at) },
            { name: 'resolved', type: FieldType.time, of: (i) => parseTime(i.resolved_at) },
            { name: 'duration s', type: FieldType.number, of: (i) => i.duration_secs },
            { name: 'alerts', type: FieldType.number, of: (i) => i.alerts_delivered },
          ]),
        ];
      }

      case 'topIssues': {
        if (!q.app) {
          return [];
        }
        const data = await this.get<{
          top_issues: Array<{ id: number; title: string; level: string; count: number; last_seen: string }>;
        }>('/api/0/analytics', { project: q.app, ...window });
        return [
          tableFrame(q.refId, data.top_issues, [
            { name: 'issue', type: FieldType.string, of: (i) => i.title },
            { name: 'level', type: FieldType.string, of: (i) => i.level },
            { name: 'events', type: FieldType.number, of: (i) => i.count },
            { name: 'last seen', type: FieldType.time, of: (i) => parseTime(i.last_seen) },
            { name: 'id', type: FieldType.number, of: (i) => i.id },
          ]),
        ];
      }

      case 'lookup': {
        const tagValue = interp(q.tagValue);
        const trace = interp(q.trace);
        if (!tagValue && !trace) {
          return []; // nothing to look up yet — an empty editor is not an error
        }
        const params: Record<string, unknown> = { ...window, limit: q.limit ?? 100 };
        if (tagValue) {
          params.tag = `${q.tagKey || 'correlation_id'}:${tagValue}`;
        }
        if (trace) {
          params.trace = trace;
        }
        if (q.app) {
          params.project = q.app;
        }
        const data = await this.get<{ events: Found[] }>('/api/0/events/search', params);
        const frame = tableFrame(q.refId, data.events, [
          { name: 'received', type: FieldType.time, of: (e: Found) => Date.parse(e.received_at) },
          { name: 'issue', type: FieldType.string, of: (e) => e.issue_title },
          { name: 'level', type: FieldType.string, of: (e) => e.level },
          { name: 'app', type: FieldType.string, of: (e) => e.project },
          { name: 'environment', type: FieldType.string, of: (e) => e.environment },
          { name: 'event id', type: FieldType.string, of: (e) => e.event_id },
          { name: 'url', type: FieldType.string, of: (e) => e.url },
        ]);
        // Clicking the issue opens the page that explains it, in a new tab. The link
        // target comes from the engine so every consumer deep-links identically.
        const issueField = frame.fields.find((f) => f.name === 'issue');
        if (issueField) {
          issueField.config = {
            links: [{ title: 'Open in bettersentryio', url: '${__data.fields.url}', targetBlank: true }],
          };
        }
        return [frame];
      }

      default:
        return [];
    }
  }

  async testDatasource() {
    try {
      const apps = await this.listApps();
      return {
        status: 'success',
        message: `Connected. ${apps.length} app${apps.length === 1 ? '' : 's'} found.`,
      };
    } catch (err) {
      const detail =
        err && typeof err === 'object' && 'data' in err
          ? JSON.stringify((err as { data: unknown }).data)
          : String(err);
      return { status: 'error', message: `Cannot reach the engine through the proxy: ${detail}` };
    }
  }
}

/* ---- frame helpers ------------------------------------------------------------ */

type Col<T> = { name: string; type: FieldType; of: (row: T) => unknown };

function tableFrame<T>(refId: string | undefined, rows: T[], cols: Array<Col<T>>): DataFrame {
  return createDataFrame({
    refId,
    fields: cols.map((c) => ({ name: c.name, type: c.type, values: rows.map(c.of) })),
  });
}

function parseTime(iso: string | null | undefined): number | null {
  return iso ? Date.parse(iso) : null;
}

/* ---- engine shapes the frames read --------------------------------------------- */

type Issue = {
  id: number;
  title: string;
  culprit: string;
  level: string;
  environment: string;
  times_seen: number;
  first_seen: string;
  last_seen: string;
  resolved_at: string | null;
};

function issueFrame(refId: string | undefined, issues: Issue[]): DataFrame {
  return tableFrame(refId, issues, [
    { name: 'issue', type: FieldType.string, of: (i) => i.title },
    { name: 'culprit', type: FieldType.string, of: (i) => i.culprit },
    { name: 'level', type: FieldType.string, of: (i) => i.level },
    { name: 'environment', type: FieldType.string, of: (i) => i.environment },
    // A real number — sortable, gauge-able. The Sentry datasource returns this
    // column as a string and every dashboard has to convert it by hand.
    { name: 'events', type: FieldType.number, of: (i) => i.times_seen },
    { name: 'first seen', type: FieldType.time, of: (i) => parseTime(i.first_seen) },
    { name: 'last seen', type: FieldType.time, of: (i) => parseTime(i.last_seen) },
    { name: 'status', type: FieldType.string, of: (i) => (i.resolved_at ? 'resolved' : 'open') },
    { name: 'id', type: FieldType.number, of: (i) => i.id },
  ]);
}

type Monitor = {
  slug: string;
  app: string;
  status: string;
  environment: string;
  last_beat_at: string | null;
  uptime_pct: number;
  beats_24h: number;
};

type Incident = {
  monitor: string;
  kind: string;
  opened_at: string;
  resolved_at: string | null;
  duration_secs: number;
  alerts_delivered: number;
};

type Found = {
  received_at: string;
  issue_title: string;
  level: string;
  project: string;
  environment: string;
  event_id: string;
  url: string;
};
