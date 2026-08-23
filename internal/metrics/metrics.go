// Package metrics is a hand-rolled Prometheus exposition endpoint.
//
// Hand-rolled deliberately: PLAN D2a caps this binary at one Go dependency (pgx), and
// the part of client_golang we would use — atomic counters and the text format — is a
// page of code. What we give up is histograms; the counters and callback gauges below
// are the set an operator actually alerts on, and latency distribution belongs to the
// ingress/service mesh which already measures it from outside.
//
// Everything registers into one package-level registry, because the process has
// exactly one metrics endpoint and threading a registry through every constructor
// would be ceremony without a second reader.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type metric struct {
	name string
	help string
	// counter: a live atomic. gauge: read through fn at scrape time, so a gauge is
	// always current and never needs a background updater.
	counter *atomic.Uint64
	gauge   func() float64
	// vec: one atomic per label value, created on first use.
	label string
	vec   map[string]*atomic.Uint64
}

var (
	mu       sync.Mutex
	registry []*metric
	byName   = map[string]*metric{}
)

func register(m *metric) *metric {
	mu.Lock()
	defer mu.Unlock()
	if existing, ok := byName[m.name]; ok {
		return existing // re-registering is a no-op, so wiring code can be naive
	}
	byName[m.name] = m
	registry = append(registry, m)
	return m
}

// Counter is a monotonic count with no labels.
type Counter struct{ v *atomic.Uint64 }

func (c Counter) Inc()          { c.v.Add(1) }
func (c Counter) Add(n uint64)  { c.v.Add(n) }
func (c Counter) Value() uint64 { return c.v.Load() }

func NewCounter(name, help string) Counter {
	m := register(&metric{name: name, help: help, counter: &atomic.Uint64{}})
	return Counter{v: m.counter}
}

// CounterVec is a counter split by one label. One label, on purpose: every extra
// label multiplies cardinality, and the callers here need exactly one dimension
// (a code class, an outcome, a kind).
type CounterVec struct{ m *metric }

func NewCounterVec(name, help, label string) CounterVec {
	m := register(&metric{name: name, help: help, label: label, vec: map[string]*atomic.Uint64{}})
	return CounterVec{m: m}
}

func (c CounterVec) With(value string) Counter {
	mu.Lock()
	v, ok := c.m.vec[value]
	if !ok {
		v = &atomic.Uint64{}
		c.m.vec[value] = v
	}
	mu.Unlock()
	return Counter{v: v}
}

// NewGauge registers a callback gauge: fn runs at scrape time. Use it for anything
// that already knows its own value (pool stats, queue depth, tick age) — a stored
// gauge would just be a stale copy of the same number.
func NewGauge(name, help string, fn func() float64) {
	register(&metric{name: name, help: help, gauge: fn})
}

// Handler renders the registry in the Prometheus text format, sorted so consecutive
// scrapes diff cleanly.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		mu.Lock()
		ms := make([]*metric, len(registry))
		copy(ms, registry)
		mu.Unlock()

		var b strings.Builder
		for _, m := range ms {
			kind := "counter"
			if m.gauge != nil {
				kind = "gauge"
			}
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", m.name, m.help, m.name, kind)
			switch {
			case m.gauge != nil:
				fmt.Fprintf(&b, "%s %g\n", m.name, m.gauge())
			case m.counter != nil:
				fmt.Fprintf(&b, "%s %d\n", m.name, m.counter.Load())
			default:
				mu.Lock()
				keys := make([]string, 0, len(m.vec))
				for k := range m.vec {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&b, "%s{%s=%q} %d\n", m.name, m.label, k, m.vec[k].Load())
				}
				mu.Unlock()
			}
		}
		_, _ = w.Write([]byte(b.String()))
	}
}
