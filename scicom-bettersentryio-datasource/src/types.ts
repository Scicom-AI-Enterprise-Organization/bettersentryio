import type { DataQuery, DataSourceJsonData } from '@grafana/data';

/**
 * The query types mirror what the engine can actually answer well, not what Sentry's
 * API happens to expose — which is the whole reason this plugin exists. The official
 * Sentry datasource returns counts as strings, buries an issue in 35 fields, and has
 * no notion of heartbeat monitors at all.
 */
export type QueryKind =
  | 'events' // events over time for one app, split by level — the volume panel
  | 'issues' // the issue table for one app, windowed
  | 'monitors' // every heartbeat monitor and its state — the flagship data
  | 'incidents' // the incident log
  | 'topIssues' // the leaderboard from /api/0/analytics
  | 'lookup'; // correlation id / trace id → the exact events

export interface BsioQuery extends DataQuery {
  kind: QueryKind;
  /** App slug. Required for events/issues/topIssues; optional filter for lookup. */
  app?: string;
  /** issues: include resolved/archived rows too. */
  includeResolved?: boolean;
  /**
   * lookup: the per-event tag to match. Key defaults to correlation_id because that
   * is the id a log line hands you; value and trace both take dashboard variables.
   */
  tagKey?: string;
  tagValue?: string;
  trace?: string;
  limit?: number;
}

export const DEFAULT_QUERY: Partial<BsioQuery> = {
  kind: 'events',
  tagKey: 'correlation_id',
};

export interface BsioDataSourceOptions extends DataSourceJsonData {
  /** Engine base URL — the data proxy route template reads it from jsonData. */
  url?: string;
}

export interface BsioSecureJsonData {
  /** A bsiot_… token from Settings → API tokens. Read-only by construction. */
  apiToken?: string;
}
