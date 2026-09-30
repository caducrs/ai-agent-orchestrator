package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var script = redis.NewScript(`
local tat = tonumber(redis.call('GET', KEYS[1]) or '0')
local now = tonumber(ARGV[1])
local emission = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
if tat < now then tat = now end
local allow_at = tat - ((burst - 1) * emission)
if now < allow_at then
  return {0, math.ceil((allow_at - now) / 1000)}
end
local new_tat = tat + emission
local ttl = math.ceil((new_tat - now) + (burst * emission))
redis.call('SET', KEYS[1], new_tat, 'PX', ttl)
return {1, 0}
`)

type Limiter struct {
	client   *redis.Client
	rate     int64
	burst    int64
	keySpace string
}

func New(client *redis.Client, rate, burst int64) *Limiter {
	return &Limiter{client: client, rate: rate, burst: burst, keySpace: "orchestrator:rate:"}
}

func (l *Limiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	emission := int64(time.Second/time.Millisecond) / l.rate
	if emission < 1 {
		emission = 1
	}
	result, err := script.Run(ctx, l.client, []string{l.keySpace + key}, time.Now().UnixMilli(), emission, l.burst).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("rate limiter: %w", err)
	}
	if len(result) != 2 {
		return false, 0, fmt.Errorf("invalid rate limiter response")
	}
	allowed, ok := result[0].(int64)
	if !ok {
		return false, 0, fmt.Errorf("invalid rate limiter decision")
	}
	retryMilliseconds, ok := result[1].(int64)
	if !ok {
		return false, 0, fmt.Errorf("invalid rate limiter retry")
	}
	return allowed == 1, time.Duration(retryMilliseconds) * time.Millisecond, nil
}

func (l *Limiter) Ping(ctx context.Context) error { return l.client.Ping(ctx).Err() }
