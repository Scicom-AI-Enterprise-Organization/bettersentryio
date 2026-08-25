import { BsioDataSource } from './datasource';

// The datasource is frontend-only, so its wire behaviour is entirely "what params
// did it put on the proxy request" — which is exactly what these tests pin.
const fetchMock = jest.fn();

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: () => ({ fetch: fetchMock }),
  getTemplateSrv: () => ({ replace: (v: string) => v }),
}));

function makeDataSource(): BsioDataSource {
  return new BsioDataSource({ url: '/api/datasources/proxy/uid/bsio-native' } as any);
}

function request(target: Record<string, unknown>) {
  return {
    range: {
      from: { toISOString: () => '2026-08-25T00:00:00.000Z' },
      to: { toISOString: () => '2026-08-25T12:00:00.000Z' },
    },
    intervalMs: 60_000,
    scopedVars: {},
    targets: [{ refId: 'A', ...target }],
  } as any;
}

function respondWith(data: unknown) {
  const { of } = require('rxjs');
  fetchMock.mockReturnValue(of({ data }));
}

beforeEach(() => fetchMock.mockReset());

describe('identity queries are not windowed', () => {
  // The bug this guards: lookup sent the panel's start/end, so the trace-correlation
  // click returned "no data" for any trace older than the pane's time range. The id
  // is exact; the person pasting it rarely knows when the event happened.
  it('lookup sends no start/end', async () => {
    respondWith({ events: [] });
    await makeDataSource().query(request({ kind: 'lookup', trace: 'abc123' }));
    const { params } = fetchMock.mock.calls[0][0];
    expect(params.trace).toBe('abc123');
    expect(params).not.toHaveProperty('start');
    expect(params).not.toHaveProperty('end');
  });

  it('eventDetail sends no start/end', async () => {
    respondWith({ events: [] });
    await makeDataSource().query(request({ kind: 'eventDetail', trace: 'abc123' }));
    const { params } = fetchMock.mock.calls[0][0];
    expect(params).not.toHaveProperty('start');
    expect(params).not.toHaveProperty('end');
  });

  it('events IS windowed — there, time is the axis, not a filter on identity', async () => {
    respondWith({ levels: [], buckets: [] });
    await makeDataSource().query(request({ kind: 'events', app: 'default' }));
    const { params } = fetchMock.mock.calls[0][0];
    expect(params.start).toBe('2026-08-25T00:00:00.000Z');
    expect(params.end).toBe('2026-08-25T12:00:00.000Z');
  });
});

describe('pasted ids are trimmed', () => {
  // Ids arrive by copy-paste — from a log line, a Grafana cell, a ticket — and a
  // trailing space makes an exact-match search silently empty.
  it('trims the trace id', async () => {
    respondWith({ events: [] });
    await makeDataSource().query(request({ kind: 'lookup', trace: '  abc123  ' }));
    expect(fetchMock.mock.calls[0][0].params.trace).toBe('abc123');
  });

  it('trims the tag value and keeps the default tag key', async () => {
    respondWith({ events: [] });
    await makeDataSource().query(request({ kind: 'lookup', tagValue: ' cid-1 ' }));
    expect(fetchMock.mock.calls[0][0].params.tag).toBe('correlation_id:cid-1');
  });

  it('an id that trims to nothing runs no query at all', async () => {
    const result = await makeDataSource().query(request({ kind: 'lookup', trace: '   ' }));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.data).toEqual([]);
  });
});
