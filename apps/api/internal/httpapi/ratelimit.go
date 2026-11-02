package httpapi

import (
	"math"
	"strconv"
	"sync"
	"time"

	"docibis/internal/store"
)

// rateLimits keeps a bucket per API key: perMinute calls, refilled evenly,
// with a burst of a full minute.
type rateLimits struct {
	mu      sync.Mutex
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// allow takes a call from the bucket of a key; when empty, it returns how
// long to wait for the next one.
func (r *rateLimits) allow(id int64, perMinute int, now time.Time) (bool, time.Duration) {
	if perMinute <= 0 {
		return true, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buckets == nil {
		r.buckets = map[int64]*bucket{}
	}
	b, ok := r.buckets[id]
	if !ok {
		b = &bucket{tokens: float64(perMinute), at: now}
		r.buckets[id] = b
	}
	perSecond := float64(perMinute) / 60
	b.tokens = math.Min(float64(perMinute), b.tokens+now.Sub(b.at).Seconds()*perSecond)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / perSecond * float64(time.Second))
}

// rateFor is the limit of a key: its own, or the setting (0: none).
func (s *Server) rateFor(k store.APIKey) int {
	if k.RatePerMinute > 0 {
		return k.RatePerMinute
	}
	return s.cfg.Load().Int("api_rate_per_minute")
}

// throttle tells whether a key may call now; otherwise the seconds to wait
// (Retry-After) and the message.
func (s *Server) throttle(k store.APIKey) (wait int, msg string) {
	limit := s.rateFor(k)
	ok, d := s.rates.allow(k.ID, limit, time.Now())
	if ok {
		return 0, ""
	}
	s.metrics.throttled()
	wait = max(1, int(math.Ceil(d.Seconds())))
	return wait, "Trop d'appels pour cette clé (" + strconv.Itoa(limit) + " par minute) : réessayez dans " + strconv.Itoa(wait) + " s."
}
