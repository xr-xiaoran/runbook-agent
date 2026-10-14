package main

import (
	"encoding/json"
	"fmt"
	"github.com/xr-xiaoran/runbook-agent/internal/agent"
	"os"
)

func main() {
	b, e := os.ReadFile("eval/cases.json")
	if e != nil {
		panic(e)
	}
	var cases []struct {
		Query    string `json:"query"`
		Expected string `json:"expected"`
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		panic(e)
	}
	idx := agent.LoadIndex()
	positive, found, negative, rejected := 0, 0, 0, 0
	rr := 0.0
	for _, c := range cases {
		hits := idx.Search(c.Query, 3)
		rank := 0
		for n, h := range hits {
			if h.ID == c.Expected {
				rank = n + 1
				break
			}
		}
		if c.Expected == "" {
			negative++
			if len(hits) == 0 {
				rejected++
			}
		} else {
			positive++
			if rank > 0 {
				found++
				rr += 1 / float64(rank)
			}
		}
		fmt.Printf("rank=%d query=%s\n", rank, c.Query)
	}
	fmt.Printf("LOCAL LEXICAL FIXTURE ONLY: positives=%d recall@3=%.3f MRR@3=%.3f negative=%d empty=%d\n", positive, float64(found)/float64(positive), rr/float64(positive), negative, rejected)
	if found != positive || rejected != negative {
		os.Exit(1)
	}
}
