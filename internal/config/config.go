// Package config loads and validates the Switchyard YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Provider types understood by the gateway.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderMock      = "mock"
)

// Routing strategies for a model alias.
const (
	StrategyPriority = "priority"
	StrategyWeighted = "weighted"
)

type Config struct {
	Server     Server           `yaml:"server"`
	Admin      Admin            `yaml:"admin"`
	Redis      Redis            `yaml:"redis"`
	Postgres   Postgres         `yaml:"postgres"`
	ClickHouse ClickHouse       `yaml:"clickhouse"`
	Providers  []Provider       `yaml:"providers"`
	Models     []Model          `yaml:"models"`
	Routing    Routing          `yaml:"routing"`
	Breaker    Breaker          `yaml:"breaker"`
	Cache      Cache            `yaml:"cache"`
	Pricing    map[string]Price `yaml:"pricing"`
	LogSink    LogSink          `yaml:"logsink"`
}

type Server struct {
	Addr              string        `yaml:"addr"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ShutdownGrace     time.Duration `yaml:"shutdown_grace"`
	MaxBodyBytes      int64         `yaml:"max_body_bytes"`
}

type Admin struct {
	TokenEnv string `yaml:"token_env"`
}

type Redis struct {
	Addr     string        `yaml:"addr"`
	Timeout  time.Duration `yaml:"timeout"`
	FailOpen bool          `yaml:"fail_open"`
}

type Postgres struct {
	DSNEnv string `yaml:"dsn_env"`
}

type ClickHouse struct {
	Addr        string `yaml:"addr"`
	Database    string `yaml:"database"`
	Username    string `yaml:"username"`
	PasswordEnv string `yaml:"password_env"`
}

type Provider struct {
	Name                string        `yaml:"name"`
	Type                string        `yaml:"type"`
	BaseURL             string        `yaml:"base_url"`
	APIKeyEnv           string        `yaml:"api_key_env"`
	Timeout             time.Duration `yaml:"timeout"`
	ConnectTimeout      time.Duration `yaml:"connect_timeout"`
	MaxIdleConnsPerHost int           `yaml:"max_idle_conns_per_host"`
}

type Model struct {
	Alias    string   `yaml:"alias"`
	Strategy string   `yaml:"strategy"`
	Targets  []Target `yaml:"targets"`
}

type Target struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Weight   int    `yaml:"weight"`
}

// Key returns the canonical "provider/model" identifier used for pricing,
// metrics and logs.
func (t Target) Key() string { return t.Provider + "/" + t.Model }

type Routing struct {
	MaxAttempts      int           `yaml:"max_attempts"`
	RetryBackoff     Backoff       `yaml:"retry_backoff"`
	RetryOn          []string      `yaml:"retry_on"`
	FirstByteTimeout time.Duration `yaml:"first_byte_timeout"`
}

type Backoff struct {
	Base   time.Duration `yaml:"base"`
	Max    time.Duration `yaml:"max"`
	Jitter bool          `yaml:"jitter"`
}

type Breaker struct {
	FailureThreshold    int           `yaml:"failure_threshold"`
	OpenDuration        time.Duration `yaml:"open_duration"`
	HalfOpenMaxRequests int           `yaml:"half_open_max_requests"`
}

type Cache struct {
	Enabled                 bool          `yaml:"enabled"`
	TTL                     time.Duration `yaml:"ttl"`
	OnlyWhenTemperatureZero bool          `yaml:"only_when_temperature_zero"`
	MaxEntryBytes           int           `yaml:"max_entry_bytes"`
}

// Price is USD per one million tokens.
type Price struct {
	Input  float64 `yaml:"input"`
	Output float64 `yaml:"output"`
}

type LogSink struct {
	QueueSize     int           `yaml:"queue_size"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}

// Load reads the YAML file at path, applies defaults and environment
// overrides, and validates the result. Warnings are non-fatal issues the
// caller should log.
func Load(path string) (*Config, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw, os.Getenv)
}

// Parse is Load without file I/O; getenv is injected for tests.
func Parse(raw []byte, getenv func(string) string) (*Config, []string, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.applyEnv(getenv)
	warnings, err := c.validate(getenv)
	if err != nil {
		return nil, warnings, err
	}
	return &c, warnings, nil
}

func (c *Config) applyDefaults() {
	setDur := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	setInt := func(i *int, v int) {
		if *i == 0 {
			*i = v
		}
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":8787"
	}
	setDur(&c.Server.ReadHeaderTimeout, 5*time.Second)
	setDur(&c.Server.ShutdownGrace, 20*time.Second)
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 2 << 20
	}
	if c.Admin.TokenEnv == "" {
		c.Admin.TokenEnv = "SWITCHYARD_ADMIN_TOKEN"
	}
	setDur(&c.Redis.Timeout, 50*time.Millisecond)
	for i := range c.Providers {
		p := &c.Providers[i]
		setDur(&p.Timeout, 60*time.Second)
		setDur(&p.ConnectTimeout, 3*time.Second)
		setInt(&p.MaxIdleConnsPerHost, 256)
	}
	for i := range c.Models {
		if c.Models[i].Strategy == "" {
			c.Models[i].Strategy = StrategyPriority
		}
	}
	setInt(&c.Routing.MaxAttempts, 3)
	setDur(&c.Routing.RetryBackoff.Base, 100*time.Millisecond)
	setDur(&c.Routing.RetryBackoff.Max, time.Second)
	setDur(&c.Routing.FirstByteTimeout, 20*time.Second)
	setInt(&c.Breaker.FailureThreshold, 5)
	setDur(&c.Breaker.OpenDuration, 30*time.Second)
	setInt(&c.Breaker.HalfOpenMaxRequests, 1)
	setDur(&c.Cache.TTL, 10*time.Minute)
	setInt(&c.Cache.MaxEntryBytes, 256<<10)
	setInt(&c.LogSink.QueueSize, 100_000)
	setInt(&c.LogSink.BatchSize, 5_000)
	setDur(&c.LogSink.FlushInterval, 500*time.Millisecond)
}

func (c *Config) applyEnv(getenv func(string) string) {
	if v := getenv("SWITCHYARD_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := getenv("SWITCHYARD_REDIS_ADDR"); v != "" {
		c.Redis.Addr = v
	}
	if v := getenv("SWITCHYARD_CLICKHOUSE_ADDR"); v != "" {
		c.ClickHouse.Addr = v
	}
}

func (c *Config) validate(getenv func(string) string) ([]string, error) {
	var errs []error
	var warnings []string

	providers := make(map[string]Provider, len(c.Providers))
	for _, p := range c.Providers {
		switch {
		case p.Name == "":
			errs = append(errs, errors.New("provider with empty name"))
			continue
		case providers[p.Name].Name != "":
			errs = append(errs, fmt.Errorf("duplicate provider %q", p.Name))
			continue
		}
		switch p.Type {
		case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderMock:
		default:
			errs = append(errs, fmt.Errorf("provider %q: unknown type %q", p.Name, p.Type))
		}
		if p.BaseURL == "" && p.Type != ProviderMock {
			errs = append(errs, fmt.Errorf("provider %q: base_url is required", p.Name))
		}
		providers[p.Name] = p
	}

	if len(c.Models) == 0 {
		errs = append(errs, errors.New("at least one model alias is required"))
	}
	used := make(map[string]bool)
	aliases := make(map[string]bool, len(c.Models))
	for _, m := range c.Models {
		if m.Alias == "" {
			errs = append(errs, errors.New("model with empty alias"))
			continue
		}
		if aliases[m.Alias] {
			errs = append(errs, fmt.Errorf("duplicate model alias %q", m.Alias))
			continue
		}
		aliases[m.Alias] = true
		if m.Strategy != StrategyPriority && m.Strategy != StrategyWeighted {
			errs = append(errs, fmt.Errorf("model %q: unknown strategy %q", m.Alias, m.Strategy))
		}
		if len(m.Targets) == 0 {
			errs = append(errs, fmt.Errorf("model %q: at least one target is required", m.Alias))
		}
		for _, t := range m.Targets {
			if _, ok := providers[t.Provider]; !ok {
				errs = append(errs, fmt.Errorf("model %q: unknown provider %q", m.Alias, t.Provider))
				continue
			}
			if t.Model == "" {
				errs = append(errs, fmt.Errorf("model %q: target on %q has empty model", m.Alias, t.Provider))
			}
			if m.Strategy == StrategyWeighted && t.Weight <= 0 {
				errs = append(errs, fmt.Errorf("model %q: target %s needs weight > 0", m.Alias, t.Key()))
			}
			used[t.Provider] = true
			if _, ok := c.Pricing[t.Key()]; !ok {
				warnings = append(warnings, fmt.Sprintf("no pricing for %s; cost will be recorded as 0", t.Key()))
			}
		}
	}

	for name := range used {
		p := providers[name]
		if p.APIKeyEnv != "" && getenv(p.APIKeyEnv) == "" {
			errs = append(errs, fmt.Errorf("provider %q: environment variable %s is not set", name, p.APIKeyEnv))
		}
	}

	if c.Routing.MaxAttempts < 1 {
		errs = append(errs, errors.New("routing.max_attempts must be >= 1"))
	}
	if c.LogSink.BatchSize > c.LogSink.QueueSize {
		errs = append(errs, errors.New("logsink.batch_size must be <= logsink.queue_size"))
	}
	return warnings, errors.Join(errs...)
}
