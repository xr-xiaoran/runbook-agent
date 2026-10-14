package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

type SearchCache struct{ client *redis.Client }
type cachedHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

func NewSearchCache(rawURL string) (*SearchCache, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid REDIS_URL")
	}
	opt.DialTimeout = 150 * time.Millisecond
	opt.ReadTimeout = 150 * time.Millisecond
	opt.WriteTimeout = 150 * time.Millisecond
	opt.MaxRetries = -1
	opt.ContextTimeoutEnabled = true
	return &SearchCache{client: redis.NewClient(opt)}, nil
}
func (c *SearchCache) Close() error { return c.client.Close() }

// The key includes corpus content and algorithm version, not only the query.
func (i *Index) cacheKey(q string, k int) string {
	b, _ := json.Marshal(i.Chunks)
	h := sha256.New()
	h.Write(b)
	fmt.Fprintf(h, "\x00bm25-v1\x00%d\x00%s", k, q)
	return "runbook:search:" + hex.EncodeToString(h.Sum(nil))
}

// Redis is optional: an outage, invalid entry or old corpus never blocks retrieval.
// Only IDs/scores are cached. Text is always resolved from this process's corpus.
func (i *Index) SearchContext(ctx context.Context, q string, k int) []Hit {
	if k < 1 || k > 5 {
		k = 3
	}
	if i.Cache == nil {
		return i.Search(q, k)
	}
	key := i.cacheKey(q, k)
	readCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	b, err := i.Cache.client.Get(readCtx, key).Bytes()
	cancel()
	if err == nil && len(b) <= 4096 {
		var refs []cachedHit
		if json.Unmarshal(b, &refs) == nil && refs != nil && len(refs) <= k {
			hits := []Hit{}
			valid := true
			seen := map[string]bool{}
			for _, ref := range refs {
				chunk, ok := i.Get(ref.ID)
				if !ok || seen[ref.ID] || ref.Score <= 0 || math.IsNaN(ref.Score) || math.IsInf(ref.Score, 0) {
					valid = false
					break
				}
				seen[ref.ID] = true
				hits = append(hits, Hit{Chunk: chunk, Score: ref.Score})
			}
			if valid {
				return hits
			}
		}
	}
	hits := i.Search(q, k)
	refs := make([]cachedHit, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, cachedHit{hit.ID, hit.Score})
	}
	b, _ = json.Marshal(refs)
	writeCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	_ = i.Cache.client.Set(writeCtx, key, b, 5*time.Minute).Err()
	return hits
}
