package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// rollupThreshold: ranges longer than this read the per-minute rollup.
const rollupThreshold = 2 * time.Hour

// Filter narrows analytics queries.
type Filter struct {
	From, To time.Time
	Project  string
	Model    string
	Key      string
	Status   string // "", "ok", "error", or an exact code
}

func (f Filter) useRollup() bool { return f.To.Sub(f.From) > rollupThreshold }

// where builds a WHERE clause for the raw table (or the rollup when rollup
// is true; it lacks key/status columns).
func (f Filter) where(rollup bool) (string, []any) {
	var conds []string
	var args []any
	if rollup {
		conds = append(conds, "minute >= toStartOfMinute(?)", "minute < ?")
	} else {
		conds = append(conds, "ts >= ?", "ts < ?")
	}
	args = append(args, f.From, f.To)
	if f.Project != "" {
		conds, args = append(conds, "project_id = ?"), append(args, f.Project)
	}
	if f.Model != "" {
		conds, args = append(conds, "model_alias = ?"), append(args, f.Model)
	}
	if !rollup {
		if f.Key != "" {
			conds, args = append(conds, "key_id = ?"), append(args, f.Key)
		}
		switch f.Status {
		case "":
		case "ok":
			conds = append(conds, "status < 400")
		case "error":
			conds = append(conds, "status >= 400")
		default:
			if n, err := strconv.Atoi(f.Status); err == nil {
				conds, args = append(conds, "status = ?"), append(args, n)
			}
		}
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Overview is the KPI summary for a range.
type Overview struct {
	Requests         uint64     `json:"requests"`
	Errors           uint64     `json:"errors"`
	ErrorRate        float64    `json:"error_rate"`
	CacheHits        uint64     `json:"cache_hits"`
	CacheHitRate     float64    `json:"cache_hit_rate"`
	PromptTokens     uint64     `json:"prompt_tokens"`
	CompletionTokens uint64     `json:"completion_tokens"`
	CostUSD          float64    `json:"cost_usd"`
	LatencyMs        Percentile `json:"latency_ms"`
	TTFTMs           Percentile `json:"ttft_ms"` // successful streaming requests only
	Source           string     `json:"source"`  // "raw" or "rollup_1m"
	QueryMs          float64    `json:"query_ms"`
}

// Percentile holds p50/p95/p99.
type Percentile struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

func pct(v []float64) Percentile {
	if len(v) != 3 {
		return Percentile{}
	}
	return Percentile{P50: nan0(v[0]), P95: nan0(v[1]), P99: nan0(v[2])}
}

func nan0(f float64) float64 {
	if f != f { // NaN when there are no rows
		return 0
	}
	return f
}

// Analytics runs read queries against ClickHouse.
type Analytics struct {
	Conn driver.Conn
}

func (a *Analytics) Overview(ctx context.Context, f Filter) (*Overview, error) {
	start := time.Now()
	o := &Overview{Source: "raw"}
	var lat, ttft []float64
	var q string
	rollup := f.useRollup() && f.Key == "" && f.Status == ""
	where, args := f.where(rollup)
	if rollup {
		o.Source = "rollup_1m"
		q = `SELECT sum(requests), sum(errors), sum(cache_hits), sum(prompt_tokens), sum(completion_tokens), sum(cost_usd),
			quantilesMerge(0.5, 0.95, 0.99)(latency_q), quantilesMerge(0.5, 0.95, 0.99)(ttft_q)
			FROM requests_1m` + where
	} else {
		q = `SELECT count(), countIf(status >= 400), countIf(cache = 'hit'), sum(prompt_tokens), sum(completion_tokens), sum(cost_usd),
			quantiles(0.5, 0.95, 0.99)(latency_ms), quantilesIf(0.5, 0.95, 0.99)(ttft_ms, status = 200 AND stream)
			FROM requests` + where
	}
	if err := a.Conn.QueryRow(ctx, q, args...).Scan(&o.Requests, &o.Errors, &o.CacheHits, &o.PromptTokens,
		&o.CompletionTokens, &o.CostUSD, &lat, &ttft); err != nil {
		return nil, err
	}
	o.LatencyMs, o.TTFTMs = pct(lat), pct(ttft)
	if o.Requests > 0 {
		o.ErrorRate = float64(o.Errors) / float64(o.Requests)
		o.CacheHitRate = float64(o.CacheHits) / float64(o.Requests)
	}
	o.QueryMs = float64(time.Since(start).Microseconds()) / 1000
	return o, nil
}

// Point is one time bucket.
type Point struct {
	T         time.Time  `json:"t"`
	Requests  uint64     `json:"requests"`
	Errors    uint64     `json:"errors"`
	CacheHits uint64     `json:"cache_hits"`
	Tokens    uint64     `json:"tokens"`
	CostUSD   float64    `json:"cost_usd"`
	LatencyMs Percentile `json:"latency_ms"`
	TTFTMs    Percentile `json:"ttft_ms"`
}

// AutoInterval picks a bucket size giving roughly 60–120 points.
func AutoInterval(d time.Duration) time.Duration {
	for _, iv := range []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
		15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 24 * time.Hour} {
		if d/iv <= 120 {
			return iv
		}
	}
	return 24 * time.Hour
}

func (a *Analytics) Timeseries(ctx context.Context, f Filter, interval time.Duration) ([]Point, error) {
	rollup := f.useRollup() && f.Key == "" && f.Status == ""
	if rollup && interval < time.Minute {
		interval = time.Minute
	}
	secs := int(interval.Seconds())
	where, args := f.where(rollup)
	var q string
	if rollup {
		q = fmt.Sprintf(`SELECT toStartOfInterval(minute, INTERVAL %d SECOND) AS t, sum(requests), sum(errors), sum(cache_hits),
			sum(prompt_tokens) + sum(completion_tokens), sum(cost_usd),
			quantilesMerge(0.5, 0.95, 0.99)(latency_q), quantilesMerge(0.5, 0.95, 0.99)(ttft_q)
			FROM requests_1m%s GROUP BY t ORDER BY t`, secs, where)
	} else {
		q = fmt.Sprintf(`SELECT toStartOfInterval(ts, INTERVAL %d SECOND) AS t, count(), countIf(status >= 400), countIf(cache = 'hit'),
			sum(prompt_tokens) + sum(completion_tokens), sum(cost_usd),
			quantiles(0.5, 0.95, 0.99)(latency_ms), quantilesIf(0.5, 0.95, 0.99)(ttft_ms, status = 200 AND stream)
			FROM requests%s GROUP BY t ORDER BY t`, secs, where)
	}
	rows, err := a.Conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		var lat, ttft []float64
		var t time.Time
		if err := rows.Scan(&t, &p.Requests, &p.Errors, &p.CacheHits, &p.Tokens, &p.CostUSD, &lat, &ttft); err != nil {
			return nil, err
		}
		p.T, p.LatencyMs, p.TTFTMs = t.UTC(), pct(lat), pct(ttft)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Request is one logged request.
type Request struct {
	TS                time.Time `json:"ts"`
	RequestID         string    `json:"request_id"`
	ProjectID         string    `json:"project_id"`
	KeyID             string    `json:"key_id"`
	Model             string    `json:"model"`
	Target            string    `json:"target"`
	Attempts          uint8     `json:"attempts"`
	Status            uint16    `json:"status"`
	ErrorCode         string    `json:"error_code"`
	Stream            bool      `json:"stream"`
	Cache             string    `json:"cache"`
	PromptTokens      uint32    `json:"prompt_tokens"`
	CompletionTokens  uint32    `json:"completion_tokens"`
	TokensEstimated   bool      `json:"tokens_estimated"`
	CostUSD           float64   `json:"cost_usd"`
	LatencyMs         float32   `json:"latency_ms"`
	TTFTMs            float32   `json:"ttft_ms"`
	UpstreamMs        float32   `json:"upstream_ms"`
	GatewayOverheadUs uint32    `json:"gateway_overhead_us"`
}

const requestColumns = `ts, request_id, project_id, key_id, model_alias, target, attempts, status, error_code, stream, cache,
	prompt_tokens, completion_tokens, tokens_estimated, cost_usd, latency_ms, ttft_ms, upstream_ms, gateway_overhead_us`

func scanRequest(scan func(...any) error) (Request, error) {
	var r Request
	err := scan(&r.TS, &r.RequestID, &r.ProjectID, &r.KeyID, &r.Model, &r.Target, &r.Attempts, &r.Status, &r.ErrorCode,
		&r.Stream, &r.Cache, &r.PromptTokens, &r.CompletionTokens, &r.TokensEstimated, &r.CostUSD, &r.LatencyMs,
		&r.TTFTMs, &r.UpstreamMs, &r.GatewayOverheadUs)
	r.TS = r.TS.UTC()
	return r, err
}

// ErrBadCursor is returned for malformed pagination cursors.
var ErrBadCursor = errors.New("invalid cursor")

func encodeCursor(r Request) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(r.TS.UnixMilli(), 10) + ":" + r.RequestID))
}

func decodeCursor(c string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	ms, id, ok := strings.Cut(string(b), ":")
	n, err := strconv.ParseInt(ms, 10, 64)
	if !ok || err != nil || id == "" {
		return time.Time{}, "", ErrBadCursor
	}
	return time.UnixMilli(n).UTC(), id, nil
}

// Requests returns a page of requests, newest first, using keyset pagination
// on (ts, request_id) so deep pages cost the same as the first.
func (a *Analytics) Requests(ctx context.Context, f Filter, cursor string, limit int) ([]Request, string, error) {
	where, args := f.where(false)
	if cursor != "" {
		ts, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += " AND (ts, request_id) < (?, ?)"
		args = append(args, ts, id)
	}
	args = append(args, limit+1)
	rows, err := a.Conn.Query(ctx, "SELECT "+requestColumns+" FROM requests"+where+" ORDER BY ts DESC, request_id DESC LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows.Scan)
		if err != nil {
			return nil, "", err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = encodeCursor(out[limit-1])
	}
	return out, next, nil
}

// ErrNotFound is returned when a request id is unknown.
var ErrNotFound = errors.New("not found")

func (a *Analytics) Request(ctx context.Context, id string) (*Request, error) {
	row := a.Conn.QueryRow(ctx, "SELECT "+requestColumns+" FROM requests WHERE request_id = ? LIMIT 1", id)
	r, err := scanRequest(row.Scan)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

// Group is one row of a breakdown.
type Group struct {
	Key          string  `json:"key"`
	Requests     uint64  `json:"requests"`
	Errors       uint64  `json:"errors"`
	Tokens       uint64  `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyP95Ms float64 `json:"latency_p95_ms"`
}

var breakdownColumns = map[string]string{
	"model": "model_alias", "key": "key_id", "target": "target", "status": "toString(status)",
	"project": "project_id", "cache": "cache",
}

// ErrBadDimension is returned for unknown breakdown dimensions.
var ErrBadDimension = errors.New("unknown breakdown dimension")

func (a *Analytics) Breakdown(ctx context.Context, f Filter, by string) ([]Group, error) {
	col, ok := breakdownColumns[by]
	if !ok {
		return nil, ErrBadDimension
	}
	where, args := f.where(false)
	rows, err := a.Conn.Query(ctx, `SELECT `+col+` AS k, count(), countIf(status >= 400), sum(prompt_tokens) + sum(completion_tokens),
		sum(cost_usd), quantile(0.95)(latency_ms) FROM requests`+where+` GROUP BY k ORDER BY count() DESC LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Key, &g.Requests, &g.Errors, &g.Tokens, &g.CostUSD, &g.LatencyP95Ms); err != nil {
			return nil, err
		}
		g.LatencyP95Ms = nan0(g.LatencyP95Ms)
		out = append(out, g)
	}
	return out, rows.Err()
}
