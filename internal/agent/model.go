package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Call struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Calls   []Call `json:"tool_calls,omitempty"`
	CallID  string `json:"tool_call_id,omitempty"`
}
type Reply struct {
	Message Message
	Tokens  int
}
type Model interface {
	Next(context.Context, []Message) (Reply, error)
	Name() string
}

var toolSpecs = []map[string]any{
	{"type": "function", "function": map[string]any{"name": "search_docs", "description": "Search local runbooks; returns source chunks, not live metrics.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]string{"type": "string"}}, "required": []string{"query"}, "additionalProperties": false}}},
	{"type": "function", "function": map[string]any{"name": "read_chunk", "description": "Read a known runbook chunk ID.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]string{"type": "string"}}, "required": []string{"id"}, "additionalProperties": false}}},
}

type Remote struct {
	Endpoint, Key, Model string
	Client               *http.Client
}

func (r *Remote) Name() string { return "remote:" + r.Model }
func (r *Remote) Next(ctx context.Context, m []Message) (Reply, error) {
	body, _ := json.Marshal(map[string]any{"model": r.Model, "messages": m, "tools": toolSpecs, "tool_choice": "auto", "max_tokens": 1200, "stream": false})
	req, e := http.NewRequestWithContext(ctx, "POST", r.Endpoint, bytes.NewReader(body))
	if e != nil {
		return Reply{}, errors.New("invalid model endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+r.Key)
	req.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(req)
	if e != nil {
		return Reply{}, errors.New("model request failed or timed out")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 262145))
	if e != nil || len(b) > 262144 {
		return Reply{}, errors.New("model response too large or unreadable")
	}
	if resp.StatusCode != 200 {
		return Reply{}, fmt.Errorf("model HTTP %d", resp.StatusCode)
	}
	var wire struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(b, &wire) != nil || len(wire.Choices) != 1 {
		return Reply{}, errors.New("invalid model response")
	}
	msg := wire.Choices[0].Message
	msg.Role = "assistant"
	return Reply{msg, wire.Usage.Total}, nil
}

// Demo is a deterministic fixture, never represented as a real language model.
type Demo struct{}

func (Demo) Name() string { return "demo-scripted-no-llm" }
func (Demo) Next(ctx context.Context, m []Message) (Reply, error) {
	if e := ctx.Err(); e != nil {
		return Reply{}, e
	}
	if m[len(m)-1].Role == "user" {
		args, _ := json.Marshal(map[string]string{"query": m[len(m)-1].Content})
		return Reply{Message: Message{Role: "assistant", Calls: []Call{{ID: "demo-search", Type: "function", Function: Function{"search_docs", string(args)}}}}}, nil
	}
	var hits []Hit
	if e := json.Unmarshal([]byte(m[len(m)-1].Content), &hits); e != nil {
		return Reply{}, e
	}
	a := Answer{Summary: "资料不足，无法确定原因。请补充项目名、错误状态和不含密钥的事件。", Citations: []string{}, Steps: []string{}}
	if len(hits) > 0 {
		a.Summary = "演示模式：以下是检索结果摘录，不是已确认的现场根因。"
		for _, h := range hits {
			a.Citations = append(a.Citations, h.ID)
			a.Steps = append(a.Steps, h.Text)
		}
		a.Draft = &Draft{Title: "排查请求（演示草稿）", Body: strings.Join(a.Steps, "\n")}
	}
	if _, raw, ok := strings.Cut(m[0].Content, "LIVE_SNAPSHOT="); ok {
		var live LiveSnapshot
		if json.Unmarshal([]byte(raw), &live) == nil {
			a.Summary = fmt.Sprintf("实时数据库快照：抽奖 %d，结果 %s，发奖状态 %s，已尝试 %d 次，版本 %d。演示模式给出规则化说明，不是模型推理，也未检查 worker 进程。", live.DrawID, live.Outcome, live.Status, live.Attempt, live.Version)
			a.Citations = append(a.Citations, fmt.Sprintf("live:draw:%d", live.DrawID))
			if live.Status == "dead" {
				a.Steps = append(a.Steps, "先排除失败原因，再由人工确认重放；重放只重新排队。fail_first=5 不变时仍会失败。")
			}
		}
	}
	b, _ := json.Marshal(a)
	return Reply{Message: Message{Role: "assistant", Content: string(b)}}, nil
}
