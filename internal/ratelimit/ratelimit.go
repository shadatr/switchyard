// Package ratelimit enforces per-key request/token rate limits and monthly
// budgets in Redis. Admission is one pipelined round trip; settlement after
// the response is asynchronous. Redis failures fail open.
package ratelimit

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/metrics"
	"github.com/shadatr/switchyard/internal/store"
)

//go:embed bucket.lua
var bucketLua string

var bucketScript = redis.NewScript(bucketLua)

// Limit kinds.
const (
	KindRPM    = "rpm"
	KindTPM    = "tpm"
	KindBudget = "budget"
)

// Decision is the outcome of Admit.
type Decision struct {
	Allowed    bool
	Kind       string        // which limit rejected the request
	RetryAfter time.Duration // for rpm/tpm rejections

	// For response headers (-1 = no limit).
	LimitRequests, RemainingRequests int
	LimitTokens, RemainingTokens     int

	// TokensReserved is what was taken from the TPM bucket; pass it to Settle.
	TokensReserved int
	// Degraded is true when Redis was unreachable and the request was let
	// through without enforcement.
	Degraded bool
}

// Limiter enforces limits for API keys.
type Limiter struct {
	rdb     redis.UniversalClient
	timeout time.Duration
	metrics *metrics.Metrics
	log     *slog.Logger
	now     func() time.Time

	lastLog atomic.Int64 // unix nanos of the last dependency-error log line
}

// New returns a Limiter. timeout bounds every Redis call on the hot path.
func New(rdb redis.UniversalClient, timeout time.Duration, m *metrics.Metrics, log *slog.Logger) *Limiter {
	if log == nil {
		log = slog.Default()
	}
	return &Limiter{rdb: rdb, timeout: timeout, metrics: m, log: log, now: time.Now}
}

func rpmKey(k *store.Key) string { return "rl:" + k.ID + ":rpm" }
func tpmKey(k *store.Key) string { return "rl:" + k.ID + ":tpm" }

// BudgetKey is the Redis key holding a key's spend (micro-USD) for the month.
func BudgetKey(keyID string, t time.Time) string {
	return "budget:" + keyID + ":" + t.UTC().Format("200601")
}

// Admit checks RPM, TPM (reserving estimatedTokens) and budget in one round trip.
func (l *Limiter) Admit(ctx context.Context, k *store.Key, estimatedTokens int) Decision {
	return l.admit(ctx, k, estimatedTokens, false)
}

func (l *Limiter) admit(ctx context.Context, k *store.Key, estimatedTokens int, retried bool) Decision {
	d := Decision{Allowed: true, LimitRequests: -1, RemainingRequests: -1, LimitTokens: -1, RemainingTokens: -1}
	if k.RPMLimit == nil && k.TPMLimit == nil && k.MonthlyBudgetUSD == nil {
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	var rpmCmd, tpmCmd *redis.Cmd
	var budgetCmd *redis.StringCmd
	_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		if k.RPMLimit != nil {
			rpmCmd = bucketScript.EvalSha(ctx, p, []string{rpmKey(k)}, *k.RPMLimit, perMinute(*k.RPMLimit), 1, 0)
		}
		if k.TPMLimit != nil {
			cost := min(estimatedTokens, *k.TPMLimit)
			tpmCmd = bucketScript.EvalSha(ctx, p, []string{tpmKey(k)}, *k.TPMLimit, perMinute(*k.TPMLimit), cost, 0)
		}
		if k.MonthlyBudgetUSD != nil {
			budgetCmd = p.Get(ctx, BudgetKey(k.ID, l.now()))
		}
		return nil
	})
	if isNoScript(err) && !retried {
		// First use on this Redis: load the script and retry once.
		if lerr := bucketScript.Load(ctx, l.rdb).Err(); lerr == nil {
			return l.admit(ctx, k, estimatedTokens, true)
		}
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		l.dependencyError(err)
		d.Degraded = true
		return d
	}

	if rpmCmd != nil {
		ok, remaining, retry, err := bucketResult(rpmCmd)
		if err != nil {
			l.dependencyError(err)
			d.Degraded = true
			return d
		}
		d.LimitRequests, d.RemainingRequests = *k.RPMLimit, remaining
		if !ok {
			d.Allowed, d.Kind, d.RetryAfter = false, KindRPM, retry
		}
	}
	if tpmCmd != nil {
		ok, remaining, retry, err := bucketResult(tpmCmd)
		if err != nil {
			l.dependencyError(err)
			d.Degraded = true
			return d
		}
		d.LimitTokens, d.RemainingTokens = *k.TPMLimit, remaining
		if ok {
			d.TokensReserved = min(estimatedTokens, *k.TPMLimit)
		} else if d.Allowed {
			d.Allowed, d.Kind, d.RetryAfter = false, KindTPM, retry
		}
	}
	if budgetCmd != nil && d.Allowed {
		spent, err := budgetCmd.Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			l.dependencyError(err)
			d.Degraded = true
			return d
		}
		if float64(spent) >= *k.MonthlyBudgetUSD*1e6 {
			d.Allowed, d.Kind = false, KindBudget
		}
	}
	// A request rejected by a later check must give back the tokens/requests
	// it took from earlier buckets.
	if !d.Allowed {
		l.refundAsync(k, d)
		if l.metrics != nil {
			l.metrics.RateLimited.WithLabelValues(d.Kind).Inc()
		}
	}
	return d
}

func (l *Limiter) refundAsync(k *store.Key, d Decision) {
	refundReq := d.Kind != KindRPM && k.RPMLimit != nil
	refundTok := d.TokensReserved
	if !refundReq && refundTok == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			if refundReq {
				bucketScript.EvalSha(ctx, p, []string{rpmKey(k)}, *k.RPMLimit, perMinute(*k.RPMLimit), -1, 1)
			}
			if refundTok > 0 {
				bucketScript.EvalSha(ctx, p, []string{tpmKey(k)}, *k.TPMLimit, perMinute(*k.TPMLimit), -refundTok, 1)
			}
			return nil
		})
		if err != nil {
			l.dependencyError(err)
		}
	}()
}

// Settle reconciles the TPM bucket with actual usage and adds the request's
// cost to the monthly spend. It runs asynchronously so it never delays the
// response.
func (l *Limiter) Settle(k *store.Key, reserved, actualTokens int, costUSD float64) {
	if k.TPMLimit == nil && k.MonthlyBudgetUSD == nil {
		return
	}
	diff := actualTokens - reserved
	micro := int64(costUSD*1e6 + 0.5)
	if (k.TPMLimit == nil || diff == 0) && (k.MonthlyBudgetUSD == nil || micro == 0) {
		return
	}
	now := l.now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			if k.TPMLimit != nil && diff != 0 {
				// force=1: take (or give back) tokens even if the bucket goes negative.
				bucketScript.EvalSha(ctx, p, []string{tpmKey(k)}, *k.TPMLimit, perMinute(*k.TPMLimit), diff, 1)
			}
			if k.MonthlyBudgetUSD != nil && micro > 0 {
				bk := BudgetKey(k.ID, now)
				p.IncrBy(ctx, bk, micro)
				p.Expire(ctx, bk, 40*24*time.Hour)
			}
			return nil
		})
		if isNoScript(err) {
			_ = bucketScript.Load(ctx, l.rdb).Err()
		}
		if err != nil {
			l.dependencyError(err)
		}
	}()
}

// Spent returns the key's spend this month in USD.
func (l *Limiter) Spent(ctx context.Context, keyID string) (float64, error) {
	v, err := l.rdb.Get(ctx, BudgetKey(keyID, l.now())).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return float64(v) / 1e6, err
}

func perMinute(limit int) string {
	return strconv.FormatFloat(float64(limit)/60000, 'g', -1, 64) // tokens per millisecond
}

func bucketResult(c *redis.Cmd) (allowed bool, remaining int, retry time.Duration, err error) {
	vals, err := c.Int64Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(vals) != 3 {
		return false, 0, 0, fmt.Errorf("ratelimit: unexpected script result %v", vals)
	}
	return vals[0] == 1, int(vals[1]), time.Duration(vals[2]) * time.Millisecond, nil
}

func isNoScript(err error) bool {
	return err != nil && redis.HasErrorPrefix(err, "NOSCRIPT")
}

func (l *Limiter) dependencyError(err error) {
	if l.metrics != nil {
		l.metrics.DependencyErrors.WithLabelValues("redis").Inc()
	}
	now := time.Now().UnixNano()
	last := l.lastLog.Load()
	if now-last > int64(10*time.Second) && l.lastLog.CompareAndSwap(last, now) {
		l.log.Warn("redis unavailable; rate limits and budgets are failing open", "err", err)
	}
}
