# Sentry grouping essentials (research notes)

> Source: read from `getsentry/sentry` @ `b815e2e0` (2026-08-07, shallow clone).
> Paths relative to the sentry repo root.
> Why we care: grouping (dedup of repeated errors into one "issue") is the single feature that
> separates an error *tracker* from a log pile. This is the minimal algorithm bettersentryio copies.

## How Sentry does it (condensed)

- Current config: `newstyle:2026-01-20` (`src/sentry/conf/server.py:2857-2861`,
  registered in `src/sentry/grouping/strategies/configurations.py:54,74`).
- Flow: event → strategies produce "variants" (`app` = in-app frames only, `system` = all frames,
  `default` for non-stacktrace) → each variant's contributing values are concatenated →
  **plain md5** (`src/sentry/grouping/utils.py:20-29`). First hash that matches an existing
  group wins; other hashes get attached to the same group
  (`src/sentry/grouping/ingest/hashing.py:165-189`) — that "list of hashes, first match wins"
  shape is what makes algorithm changes non-destructive later.
- Strategy priority (`configurations.py:19-29`): chained-exception > threads > raw stacktrace >
  template > csp > message (last resort). First contributing strategy wins.

### Exception events (`src/sentry/grouping/strategies/newstyle.py:550-641`)

Hash inputs, in order: `[error_type, error_value, stacktrace]` — but **value is suppressed
whenever the stacktrace contributes** (newstyle.py:602-607). So:

- stacktrace present → hash = type + frames
- no stacktrace → hash = type + *parameterized* value

### Per-frame inputs (`newstyle.py:300-379`)

`module` (wins) or `lowercase(basename(filename))`, plus normalized `function`
(`trim_function_name`; JS keeps last dot-segment; drops `<redacted>`/`<unknown>`).
`context_line` only contributes on javascript/node/python/php/ruby and only if ≤120 chars —
skip it in v0.

### Frame filtering (`newstyle.py:482-543`)

- Recursion: consecutive frames identical on (path, module, filename, function, lineno, colno)
  → drop the later one (newstyle.py:100-127).
- App variant: only `in_app` frames contribute; if none, fall back to the system variant.
- No hard frame cap in Python code (caps exist only as enhancement-rule vars); "keep last ~30
  frames nearest the crash" is a fine substitute for a clone.

### Message-only events (`src/sentry/grouping/strategies/message.py:19-38`)

Prefer the raw template `logentry.message` over interpolated `formatted` (free parameterization
when the SDK sends a template). Then normalize (`src/sentry/grouping/utils.py:57-115`):
drop blank lines, keep first 2 lines, append `"..."` if trimmed.

**Parameterization** (`src/sentry/grouping/parameterization.py`): one alternated regex of named
groups; each match replaced by `<group_name>`. Order matters: email, url, hostname, traceparent,
uuid, sha1, md5, date, duration, mac, ip, hex (covers 0x addresses + bare 4-128 char hex),
git sha, random ids, float, int (1-7 digits; 8+ treated as hex), quoted strings / bools only on
the RHS of `key=value`. Inputs > 8192 chars skipped (ReDoS guard).
Minimum viable set for bettersentryio: `uuid, hex/0x, int, float, email, url, ip, quoted-str`.

### Fingerprint override (`src/sentry/grouping/api.py:387-444`, `variants.py:191-271`)

- Missing → `["{{ default }}"]` → normal grouping.
- No `{{ default }}` entry → hash the fingerprint array alone (custom grouping).
- Mixed ("hybrid") → walk the fingerprint list, splice the default variant's values in place of
  `{{ default }}`, md5 the combined list.

### Title & culprit

- Title = `f"{type}: {first_line(value)[:256]}"`, fallback crash-frame `function`, then
  `"<unknown>"` (`src/sentry/eventtypes/error.py:75-82`). Main exception = **last** entry in
  `exception.values` (error.py:86-116). Message events: first line of message, 256 chars.
- Culprit = last `in_app` frame (scan in reverse; else last frame), formatted
  `"{module|filename} in {function}"` (JS: `"{function}({module|filename})"`)
  (`src/sentry/culprit.py:15-73`).

## Recommended minimal algorithm for bettersentryio

```
group_hash(event):
  fp = event.fingerprint or ["{{ default }}"]
  exc = last(event.exception.values)  if present
  if exc and exc.stacktrace:
      frames = drop_consecutive_duplicates(exc.stacktrace.frames)
      frames = [f for f in frames if f.in_app] or frames        # app, fallback system
      frames = frames[-30:]                                     # nearest the crash
      parts  = [exc.type] + flatten((f.module or lower(basename(f.filename)),
                                     trim_func(f.function)) for f in frames)
  elif exc:
      parts = [exc.type, parameterize(exc.value)]
  else:
      parts = [parameterize(first_2_lines(event.message))] or ["<fallback>"]

  if fp == ["{{ default }}"]:        values = parts
  elif "{{ default }}" not in fp:    values = fp
  else:                              values = splice(fp, "{{ default }}" -> parts)
  return md5(concat(values))
```

Storage shape: `group_hashes(project_id, hash → group_id)` unique index; on ingest, first
existing hash wins, else create group. Store both app and system hashes per event so a future
algorithm change maps old groups forward instead of splitting them.
