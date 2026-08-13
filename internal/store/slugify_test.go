package store

import "testing"

// The Add-app dialog previews the slug with a mirror of Slugify in TypeScript
// (web/src/lib/bsio.ts). If the two disagree, the dialog promises a URL the engine
// will not create and the post-create redirect 404s — so this table is the contract,
// and the same cases are asserted on the TS side.
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"TTS API":          "tts-api",
		"vLLM Serving":     "vllm-serving",
		"a+b":              "ab",
		"v1.5&2":           "v1-52",
		"Café":             "caf",
		"TTS & Voice":      "tts-voice",
		"  spaced  out  ":  "spaced-out",
		"___leading":       "leading",
		"trailing---":      "trailing",
		"C#/.NET Worker":   "c-net-worker",
		"batch(nightly)":   "batchnightly",
		"ETL — daily":      "etl-daily",
		"100% uptime":      "100-uptime",
		"tts/api":          "tts-api",
		"":                 "",
		"!!!":              "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
