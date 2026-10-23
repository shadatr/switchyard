package config

import (
	"strings"
	"testing"
	"time"
)

const validYAML = `
auth: { enabled: false }
providers:
  - name: openai
    type: openai
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
  - name: mock
    type: openai
    base_url: http://localhost:9797/v1
models:
  - alias: fast
    targets:
      - { provider: openai, model: gpt-4o-mini }
  - alias: mock-fast
    strategy: weighted
    targets:
      - { provider: mock, model: mock-a, weight: 80 }
      - { provider: mock, model: mock-b, weight: 20 }
pricing:
  "openai/gpt-4o-mini": { input: 0.15, output: 0.60 }
  "mock/mock-a": { input: 0.10, output: 0.40 }
`

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseValid(t *testing.T) {
	c, warnings, err := Parse([]byte(validYAML), env(map[string]string{"OPENAI_API_KEY": "sk-test"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Server.Addr != ":8787" || c.Server.ShutdownGrace != 20*time.Second {
		t.Errorf("defaults not applied: %+v", c.Server)
	}
	if c.Models[0].Strategy != StrategyPriority {
		t.Errorf("default strategy = %q", c.Models[0].Strategy)
	}
	if c.Providers[0].MaxIdleConnsPerHost != 256 {
		t.Errorf("provider defaults not applied: %+v", c.Providers[0])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "mock/mock-b") {
		t.Errorf("warnings = %v, want one about mock/mock-b pricing", warnings)
	}
}

func TestEnvOverride(t *testing.T) {
	c, _, err := Parse([]byte(validYAML), env(map[string]string{
		"OPENAI_API_KEY":  "sk-test",
		"SWITCHYARD_ADDR": ":9999",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Addr != ":9999" {
		t.Errorf("addr = %q, want :9999", c.Server.Addr)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing api key env",
			yaml:    validYAML,
			wantErr: "OPENAI_API_KEY is not set",
		},
		{
			name: "duplicate alias",
			yaml: `
providers: [{ name: mock, type: openai, base_url: "http://x" }]
models:
  - { alias: a, targets: [{ provider: mock, model: m }] }
  - { alias: a, targets: [{ provider: mock, model: m }] }
`,
			wantErr: `duplicate model alias "a"`,
		},
		{
			name: "unknown provider in target",
			yaml: `
providers: [{ name: mock, type: openai, base_url: "http://x" }]
models: [{ alias: a, targets: [{ provider: nope, model: m }] }]
`,
			wantErr: `unknown provider "nope"`,
		},
		{
			name: "weighted without weight",
			yaml: `
providers: [{ name: mock, type: openai, base_url: "http://x" }]
models: [{ alias: a, strategy: weighted, targets: [{ provider: mock, model: m }] }]
`,
			wantErr: "needs weight > 0",
		},
		{
			name: "unknown provider type",
			yaml: `
providers: [{ name: p, type: bogus, base_url: "http://x" }]
models: [{ alias: a, targets: [{ provider: p, model: m }] }]
`,
			wantErr: `unknown type "bogus"`,
		},
		{
			name:    "no models",
			yaml:    `providers: [{ name: p, type: openai, base_url: "http://x" }]`,
			wantErr: "at least one model alias",
		},
		{
			name:    "unknown field",
			yaml:    "servr: {}\n",
			wantErr: "field servr not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tt.yaml), env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestAuthRequiresPostgres(t *testing.T) {
	y := strings.Replace(validYAML, "auth: { enabled: false }", "postgres: { dsn_env: PG }", 1)
	if _, _, err := Parse([]byte(y), env(map[string]string{"OPENAI_API_KEY": "x"})); err == nil || !strings.Contains(err.Error(), "postgres.dsn_env") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := Parse([]byte(y), env(map[string]string{"OPENAI_API_KEY": "x", "PG": "postgres://"})); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestTargetKey(t *testing.T) {
	if got := (Target{Provider: "openai", Model: "gpt-4o"}).Key(); got != "openai/gpt-4o" {
		t.Errorf("Key() = %q", got)
	}
}
