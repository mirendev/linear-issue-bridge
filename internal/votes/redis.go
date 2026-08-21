package votes

import (
	"context"
	"errors"

	"time"

	"github.com/redis/go-redis/v9"
)

// redisBackend adapts go-redis to the narrow backend interface.
type redisBackend struct {
	client *redis.Client
}

// NewRedisBackend builds a bounded client from a redis:// or valkey:// URL.
//
// Bounded is the point. The timeouts below mean an absent or unhealthy server
// fails a command in milliseconds instead of hanging a roadmap render, while
// go-redis keeps reconnecting underneath so votes resume when it comes back.
func NewRedisBackend(url string) (backend, error) {
	opts, err := redis.ParseURL(normalizeURL(url))
	if err != nil {
		// Deliberately not %w. ParseURL returns a *url.Error carrying the raw
		// URL, and main.go logs whatever comes back, so wrapping would put the
		// Valkey password in the logs.
		return nil, errors.New("parse redis url: malformed REDIS_URL/VALKEY_URL")
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	opts.MaxRetries = 1
	return &redisBackend{client: redis.NewClient(opts)}, nil
}

// Miren's Valkey addon hands out valkey:// URLs; go-redis only parses the
// redis:// spelling of the same protocol.
func normalizeURL(url string) string {
	switch {
	case len(url) > 9 && url[:9] == "valkey://":
		return "redis://" + url[9:]
	case len(url) > 10 && url[:10] == "valkeys://":
		return "rediss://" + url[10:]
	default:
		return url
	}
}

func (r *redisBackend) MGet(ctx context.Context, keys []string) ([]any, error) {
	return r.client.MGet(ctx, keys...).Result()
}

func (r *redisBackend) Incr(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, key).Result()
}

func (r *redisBackend) Decr(ctx context.Context, key string) (int64, error) {
	return r.client.Decr(ctx, key).Result()
}

func (r *redisBackend) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return r.client.Expire(ctx, key, ttl).Err()
}

func (r *redisBackend) Del(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

func (r *redisBackend) Close() error {
	return r.client.Close()
}
