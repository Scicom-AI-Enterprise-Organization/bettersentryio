import React, { useEffect, useState } from 'react';

import { QueryEditorProps, SelectableValue } from '@grafana/data';
import { Checkbox, Combobox, ComboboxOption, InlineField, InlineFieldRow, Input, Select } from '@grafana/ui';

import { BsioDataSource } from '../datasource';
import { BsioDataSourceOptions, BsioQuery, QueryKind } from '../types';

type Props = QueryEditorProps<BsioDataSource, BsioQuery, BsioDataSourceOptions>;

const KINDS: Array<SelectableValue<QueryKind>> = [
  { value: 'events', label: 'Events over time', description: 'Per level, zero-filled buckets' },
  { value: 'issues', label: 'Issues', description: 'The issue table, windowed' },
  { value: 'monitors', label: 'Monitors', description: 'Every heartbeat monitor and its state' },
  { value: 'incidents', label: 'Incidents', description: 'The incident log' },
  { value: 'topIssues', label: 'Top issues', description: 'The leaderboard for the window' },
  { value: 'lookup', label: 'Find by correlation / trace id', description: 'Per-event identity search' },
  {
    value: 'eventDetail',
    label: 'Event detail',
    description: 'Exception, highlights, trace context, additional data, packages — for the newest matching event',
  },
];

/** The kinds that identify an event by tag / trace id and share the lookup row. */
const IDENTITY_KINDS: QueryKind[] = ['lookup', 'eventDetail'];

const NEEDS_APP: QueryKind[] = ['events', 'issues', 'topIssues'];

export function QueryEditor({ query, onChange, onRunQuery, datasource }: Props) {
  const kind = query.kind ?? 'events';
  const [apps, setApps] = useState<ComboboxOption[]>([]);

  useEffect(() => {
    let alive = true;
    datasource
      .listApps()
      .then((list) => {
        if (alive) {
          setApps(list.map((a) => ({ value: a.slug, label: a.name, description: a.slug })));
        }
      })
      .catch(() => setApps([]));
    return () => {
      alive = false;
    };
  }, [datasource]);

  const set = (patch: Partial<BsioQuery>, run = true) => {
    onChange({ ...query, ...patch });
    if (run) {
      onRunQuery();
    }
  };

  return (
    <>
      <InlineFieldRow>
        <InlineField label="Query" labelWidth={12}>
          <Select width={34} options={KINDS} value={kind} onChange={(v) => set({ kind: v.value! })} />
        </InlineField>
        {(NEEDS_APP.includes(kind) || IDENTITY_KINDS.includes(kind)) && (
          <InlineField
            label="App"
            labelWidth={8}
            tooltip={IDENTITY_KINDS.includes(kind) ? 'Optional: an id is searched across every app by default' : undefined}
          >
            <Combobox
              width={28}
              options={apps}
              value={query.app ?? null}
              isClearable={IDENTITY_KINDS.includes(kind)}
              placeholder={IDENTITY_KINDS.includes(kind) ? 'all apps' : 'select an app'}
              onChange={(v: ComboboxOption | null) => set({ app: v?.value })}
            />
          </InlineField>
        )}
        {kind === 'issues' && (
          <InlineField label="Resolved too" labelWidth={14}>
            <Checkbox value={query.includeResolved ?? false} onChange={(e) => set({ includeResolved: e.currentTarget.checked })} />
          </InlineField>
        )}
      </InlineFieldRow>

      {IDENTITY_KINDS.includes(kind) && (
        <InlineFieldRow>
          <InlineField label="Tag" labelWidth={12} tooltip="Per-event tag key; correlation_id is what a log line hands you">
            <Input
              width={18}
              value={query.tagKey ?? 'correlation_id'}
              onChange={(e) => set({ tagKey: e.currentTarget.value }, false)}
              onBlur={() => onRunQuery()}
            />
          </InlineField>
          <InlineField label="=" labelWidth={3} grow tooltip="Dashboard variables work here, e.g. $correlation_id">
            <Input
              value={query.tagValue ?? ''}
              placeholder="value, or $variable"
              onChange={(e) => set({ tagValue: e.currentTarget.value }, false)}
              onBlur={() => onRunQuery()}
            />
          </InlineField>
          <InlineField label="or trace id" labelWidth={12} grow tooltip="Matches contexts.trace.trace_id — what the SDK attaches">
            <Input
              value={query.trace ?? ''}
              placeholder="32-hex trace id, or $trace"
              onChange={(e) => set({ trace: e.currentTarget.value }, false)}
              onBlur={() => onRunQuery()}
            />
          </InlineField>
        </InlineFieldRow>
      )}
    </>
  );
}
