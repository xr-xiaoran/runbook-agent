package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type LiveSnapshot struct {
	DrawID     int64  `json:"draw_id"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	Status     string `json:"status"`
	Attempt    int    `json:"attempt"`
	Version    int64  `json:"version"`
	FailFirst  int    `json:"fail_first"`
	ObservedAt int64  `json:"observed_at"`
	Events     []struct {
		At      int64  `json:"at_ms"`
		Message string `json:"message"`
	} `json:"events"`
}
type ReplayProposal struct {
	DrawID          int64  `json:"draw_id"`
	ExpectedVersion int64  `json:"expected_version"`
	ExpiresAt       int64  `json:"expires_at"`
	Warning         string `json:"warning"`
}
type ReplayReceipt struct {
	OperationID     string `json:"operation_id"`
	DrawID          int64  `json:"draw_id"`
	ExpectedVersion int64  `json:"expected_version"`
	NewVersion      int64  `json:"new_version"`
	Status          string `json:"status"`
	Actor           string `json:"actor"`
}
type CampusClient struct {
	Base, ReadKey, ReplayKey string
	client                   *http.Client
}
type UpstreamError struct{ Status int }

func (e UpstreamError) Error() string { return fmt.Sprintf("CampusReward HTTP %d", e.Status) }
func NewCampusClient(base, read, replay string) (*CampusClient, error) {
	u, e := url.Parse(base)
	if e != nil {
		return nil, e
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(read) < 16 || len(replay) < 16 || read == replay {
		return nil, errors.New("Campus URL must be a loopback HTTP origin; distinct keys >=16 required")
	}
	return &CampusClient{base, read, replay, &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *CampusClient) request(ctx context.Context, method, path, key, operation string, body any, out any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if operation != "" {
		req.Header.Set("Idempotency-Key", operation)
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return errors.New("CampusReward unavailable; replay outcome may be unknown, retry the SAME run")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return UpstreamError{resp.StatusCode}
	}
	b, e = io.ReadAll(io.LimitReader(resp.Body, 131073))
	if e != nil || len(b) > 131072 {
		return errors.New("invalid Campus response size")
	}
	return json.Unmarshal(b, out)
}
func (c *CampusClient) Inspect(ctx context.Context, id int64) (LiveSnapshot, error) {
	var v LiveSnapshot
	e := c.request(ctx, "GET", fmt.Sprintf("/api/ops/draws/%d", id), c.ReadKey, "", nil, &v)
	return v, e
}
func (c *CampusClient) Replay(ctx context.Context, run string, p ReplayProposal) (ReplayReceipt, error) {
	var v ReplayReceipt
	e := c.request(ctx, "POST", fmt.Sprintf("/api/ops/draws/%d/replay", p.DrawID), c.ReplayKey, run, map[string]int64{"expected_version": p.ExpectedVersion}, &v)
	return v, e
}
func (c *CampusClient) Receipt(ctx context.Context, run string) (ReplayReceipt, error) {
	var v ReplayReceipt
	e := c.request(ctx, "GET", "/api/ops/operations/"+run, c.ReplayKey, "", nil, &v)
	return v, e
}

func (s *Server) ApproveReplay(ctx context.Context, id string) (ReplayReceipt, error) {
	if s.Campus == nil {
		return ReplayReceipt{}, errors.New("Campus integration not configured")
	}
	r, e := s.Store.Get(ctx, id)
	if e != nil {
		return ReplayReceipt{}, e
	}
	if r.Status != "completed" || r.Replay == nil {
		return ReplayReceipt{}, ErrNoDraft
	}
	// Look up the authoritative receipt first: handles lost responses even after proposal expiry.
	receipt, e := s.Campus.Receipt(ctx, id)
	if e == nil {
		return receipt, nil
	}
	var upstream UpstreamError
	if !errors.As(e, &upstream) || upstream.Status != 404 {
		return receipt, e
	}
	if time.Now().UnixMilli() > r.Replay.ExpiresAt {
		return receipt, errors.New("proposal expired: inspect again")
	}
	// Backend checks expected version atomically; no read-then-write race on the agent.
	return s.Campus.Replay(ctx, id, *r.Replay)
}
