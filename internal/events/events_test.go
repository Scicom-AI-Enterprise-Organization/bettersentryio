package events

import "testing"

func frame(mod, file, fn string, line int, inApp bool) Frame {
	return Frame{Module: mod, Filename: file, Function: fn, Lineno: line, InApp: inApp}
}

func exc(kind, value string, frames ...Frame) *Event {
	return &Event{
		Exception: &ExceptionValues{Values: []Exception{{
			Type:       kind,
			Value:      value,
			Stacktrace: &Stack{Frames: frames},
		}}},
	}
}

// The whole point of grouping is that many occurrences become one row. If a line shift
// splits an issue, every deploy that touches a file resets its history.
func TestLineNumbersDoNotSplitAnIssue(t *testing.T) {
	a, _, _, _ := Fingerprint(exc("RuntimeError", "boom",
		frame("app.worker", "worker.py", "run", 41, true)))
	b, _, _, _ := Fingerprint(exc("RuntimeError", "boom",
		frame("app.worker", "worker.py", "run", 58, true)))
	if a != b {
		t.Fatalf("adding lines above a function split its issue: %s != %s", a, b)
	}
}

// ...and the inverse: different places must stay different, or unrelated bugs merge and
// the count becomes meaningless.
func TestDifferentFunctionsAreDifferentIssues(t *testing.T) {
	a, _, _, _ := Fingerprint(exc("RuntimeError", "boom",
		frame("app.worker", "worker.py", "run", 41, true)))
	b, _, _, _ := Fingerprint(exc("RuntimeError", "boom",
		frame("app.worker", "worker.py", "flush", 41, true)))
	if a == b {
		t.Fatal("two different functions grouped into one issue")
	}
}

func TestDifferentExceptionTypesAreDifferentIssues(t *testing.T) {
	f := frame("app.worker", "worker.py", "run", 10, true)
	a, _, _, _ := Fingerprint(exc("ValueError", "bad", f))
	b, _, _, _ := Fingerprint(exc("TypeError", "bad", f))
	if a == b {
		t.Fatal("ValueError and TypeError grouped together")
	}
}

// A varying value must not split the issue: "batch 12 failed" and "batch 13 failed" are
// one bug seen twice.
func TestVaryingValuesGroupTogether(t *testing.T) {
	f := frame("app.worker", "worker.py", "run", 10, true)
	a, _, _, _ := Fingerprint(exc("RuntimeError", "batch 12 failed after 300ms", f))
	b, _, _, _ := Fingerprint(exc("RuntimeError", "batch 4193 failed after 12ms", f))
	if a != b {
		t.Fatal("the same bug with different numbers became two issues")
	}
}

// Two distinct bugs that both end deep inside the same library are two bugs. Grouping on
// in-app frames keeps them apart; grouping on the deepest frame would merge them.
func TestLibraryFramesDoNotMergeDistinctCallers(t *testing.T) {
	lib := frame("httpx._client", "_client.py", "send", 900, false)
	a, _, _, _ := Fingerprint(exc("ConnectError", "refused",
		frame("app.sync", "sync.py", "pull", 10, true), lib))
	b, _, _, _ := Fingerprint(exc("ConnectError", "refused",
		frame("app.push", "push.py", "publish", 22, true), lib))
	if a == b {
		t.Fatal("two callers of the same library call grouped into one issue")
	}
}

// With no in-app frames at all there is still a stack to group on, and it must be used.
func TestLibraryOnlyStacksStillGroupByLocation(t *testing.T) {
	a, _, _, _ := Fingerprint(exc("KeyError", "'x'",
		frame("json.decoder", "decoder.py", "decode", 5, false)))
	b, _, _, _ := Fingerprint(exc("KeyError", "'x'",
		frame("yaml.parser", "parser.py", "parse", 5, false)))
	if a == b {
		t.Fatal("library-only stacks in different modules grouped together")
	}
}

func TestCulpritIsTheDeepestInAppFrame(t *testing.T) {
	_, kind, culprit, title := Fingerprint(exc("RuntimeError", "model died",
		frame("app.main", "main.py", "startup", 10, true),
		frame("app.worker", "worker.py", "batching_loop", 44, true),
		frame("torch._dynamo", "eval_frame.py", "__call__", 900, false)))
	if kind != "RuntimeError" {
		t.Errorf("kind = %q", kind)
	}
	if culprit != "app.worker in batching_loop" {
		t.Errorf("culprit = %q, want the deepest in-app frame", culprit)
	}
	if title != "RuntimeError: model died" {
		t.Errorf("title = %q", title)
	}
}

// Messages without an exception still have to group, and the same log line with
// different ids is one issue.
func TestMessagesGroupAfterParameterizing(t *testing.T) {
	a, _, _, _ := Fingerprint(&Event{Logger: "app.auth", Message: "user 91 not found"})
	b, _, _, _ := Fingerprint(&Event{Logger: "app.auth", Message: "user 4711 not found"})
	if a != b {
		t.Fatal("the same log line with different ids became two issues")
	}
	c, _, _, _ := Fingerprint(&Event{Logger: "app.auth", Message: "token expired"})
	if a == c {
		t.Fatal("different log lines grouped together")
	}
}

func TestParameterize(t *testing.T) {
	cases := map[string]string{
		"user 91 not found":                          "user <n> not found",
		"batch 4f3a9c22 failed":                      "batch <hex> failed",
		"missing key 'device_id'":                    "missing key <str>",
		"at 0x7f3b2c":                                "at <addr>",
		"trace 3f2504e0-4f89-11d3-9a0c-0305e82c3301": "trace <uuid>",
	}
	for in, want := range cases {
		if got := parameterize(in); got != want {
			t.Errorf("parameterize(%q) = %q, want %q", in, got, want)
		}
	}
}
