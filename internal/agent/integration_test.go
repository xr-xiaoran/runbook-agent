package agent

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMySQLApprovalPersistence(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	s, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	id := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	result := (&Runtime{LoadIndex(), Demo{}}).Run(ctx, id, "CampusReward pending 发奖")
	if result.Status != "completed" || result.Answer.Draft == nil {
		t.Fatal(result)
	}
	if err = s.Save(ctx, "pending", result); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan int64, 12)
	errs := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Approve(ctx, id)
			if e != nil {
				errs <- e
				return
			}
			ids <- v.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Errorf("duplicate tickets %d/%d", first, id)
		}
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM tickets WHERE run_id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	reopened, e := OpenMySQL(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	got, e := reopened.Get(ctx, id)
	if e != nil || got.ID != id {
		t.Fatalf("reopen: %v", e)
	}
	if _, e = s.db.Exec("DELETE FROM tickets WHERE run_id=?", id); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("DELETE FROM runs WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
}
func TestRedisSearchFallbackAndInvalidation(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	cache, e := NewSearchCache(raw)
	if e != nil {
		t.Fatal(e)
	}
	defer cache.Close()
	idx := LoadIndex()
	idx.Cache = cache
	ctx := context.Background()
	query := fmt.Sprintf("pending %d", time.Now().UnixNano())
	key := idx.cacheKey(query, 3)
	defer cache.client.Del(ctx, key)
	expected := idx.Search(query, 3)
	got := idx.SearchContext(ctx, query, 3)
	if !reflect.DeepEqual(expected, got) {
		t.Fatal("cache miss differs")
	}
	if ttl, e := cache.client.TTL(ctx, key).Result(); e != nil || ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("ttl=%v err=%v", ttl, e)
	}
	if !reflect.DeepEqual(expected, idx.SearchContext(ctx, query, 3)) {
		t.Fatal("cache hit differs")
	}
	if e = cache.client.Set(ctx, key, `[{"id":"fabricated","score":1}]`, time.Minute).Err(); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(expected, idx.SearchContext(ctx, query, 3)) {
		t.Fatal("invalid cache did not fall back")
	}
	idx.Chunks[0].Text += " changed"
	if idx.cacheKey(query, 3) == key {
		t.Fatal("corpus change kept old key")
	}
}
func TestRedisUnavailableFallsBack(t *testing.T) {
	cache, e := NewSearchCache("redis://127.0.0.1:1/0")
	if e != nil {
		t.Fatal(e)
	}
	defer cache.Close()
	idx := LoadIndex()
	idx.Cache = cache
	expected := idx.Search("pending", 3)
	if !reflect.DeepEqual(expected, idx.SearchContext(context.Background(), "pending", 3)) {
		t.Fatal("outage changes evidence")
	}
}
