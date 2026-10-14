package main

import (
	"context"
	"github.com/xr-xiaoran/runbook-agent/internal/agent"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	key, approve := os.Getenv("API_KEY"), os.Getenv("APPROVAL_KEY")
	if len(key) < 16 || len(approve) < 16 || key == approve {
		log.Fatal("API_KEY and APPROVAL_KEY must be different and at least 16 bytes")
	}
	addr := env("ADDR", "127.0.0.1:8084")
	host, _, e := net.SplitHostPort(addr)
	if e != nil || net.ParseIP(host) == nil || (!net.ParseIP(host).IsLoopback() && !(host == "0.0.0.0" && os.Getenv("CONTAINER_MODE") == "1")) {
		log.Fatal("use loopback IP; container binding requires CONTAINER_MODE=1 and 0.0.0.0")
	}
	var model agent.Model = agent.Demo{}
	mode := env("MODEL_MODE", "demo")
	if mode != "demo" && mode != "remote" {
		log.Fatal("MODEL_MODE must be demo or remote")
	}
	if mode == "remote" {
		endpoint := os.Getenv("MODEL_ENDPOINT")
		u, e := url.Parse(endpoint)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/chat/completions") {
			log.Fatal("MODEL_ENDPOINT must be an HTTPS chat/completions URL without credentials or query")
		}
		if os.Getenv("MODEL_API_KEY") == "" || os.Getenv("MODEL_NAME") == "" {
			log.Fatal("remote mode requires MODEL_API_KEY and MODEL_NAME")
		}
		model = &agent.Remote{Endpoint: endpoint, Key: os.Getenv("MODEL_API_KEY"), Model: os.Getenv("MODEL_NAME")}
	}
	var store *agent.Store
	switch env("DB_DRIVER", "sqlite") {
	case "sqlite":
		store, e = agent.Open(env("DB_PATH", "runbook-agent.db"))
	case "mysql":
		store, e = agent.OpenMySQL(os.Getenv("MYSQL_DSN"))
	default:
		log.Fatal("DB_DRIVER must be sqlite or mysql")
	}
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	index := agent.LoadIndex()
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		index.Cache, e = agent.NewSearchCache(redisURL)
		if e != nil {
			log.Fatal(e)
		}
		defer index.Cache.Close()
	}
	app := agent.NewServer(&agent.Runtime{Index: index, Model: model}, store, key, approve)
	if base := os.Getenv("CAMPUS_URL"); base != "" {
		app.Campus, e = agent.NewCampusClient(base, os.Getenv("CAMPUS_READ_KEY"), os.Getenv("CAMPUS_REPLAY_KEY"))
		if e != nil {
			log.Fatal(e)
		}
	}
	server := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	log.Printf("RunbookAgent mode=%s url=http://%s", model.Name(), addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
