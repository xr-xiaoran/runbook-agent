package agent

import (
	"embed"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

//go:embed knowledge/*.md
var knowledge embed.FS

type Chunk struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}
type Hit struct {
	Chunk
	Score float64 `json:"score"`
}
type Index struct {
	Chunks []Chunk
	Cache  *SearchCache
}

// English words and overlapping Chinese bigrams. This is lexical, not embedding search.
func terms(s string) []string {
	var out []string
	var word []rune
	var prev rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, string(word))
			word = nil
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Han, r) {
			flush()
			if prev != 0 {
				out = append(out, string([]rune{prev, r}))
			}
			prev = r
		} else {
			prev = 0
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				word = append(word, r)
			} else {
				flush()
			}
		}
	}
	flush()
	return out
}
func LoadIndex() *Index {
	idx := &Index{}
	entries, _ := knowledge.ReadDir("knowledge")
	for _, entry := range entries {
		b, _ := knowledge.ReadFile("knowledge/" + entry.Name())
		for n, part := range strings.Split(string(b), "\n\n") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			idx.Chunks = append(idx.Chunks, Chunk{fmt.Sprintf("%s:%d", strings.TrimSuffix(entry.Name(), ".md"), n+1), entry.Name(), strings.TrimSpace(part)})
		}
	}
	return idx
}
func (i *Index) Get(id string) (Chunk, bool) {
	for _, c := range i.Chunks {
		if c.ID == id {
			return c, true
		}
	}
	return Chunk{}, false
}
func (i *Index) Search(q string, k int) []Hit {
	if k < 1 || k > 5 {
		k = 3
	}
	query := map[string]bool{}
	for _, t := range terms(q) {
		query[t] = true
	}
	df := map[string]int{}
	freq := make([]map[string]int, len(i.Chunks))
	lens := make([]int, len(i.Chunks))
	avg := 0.0
	for n, c := range i.Chunks {
		freq[n] = map[string]int{}
		for _, t := range terms(c.Text) {
			freq[n][t]++
			lens[n]++
		}
		avg += float64(lens[n])
		for t := range freq[n] {
			df[t]++
		}
	}
	if len(i.Chunks) == 0 {
		return []Hit{}
	}
	avg /= float64(len(i.Chunks))
	if avg == 0 {
		return []Hit{}
	}
	hits := []Hit{}
	for n, c := range i.Chunks {
		score := 0.0
		for t := range query {
			f := float64(freq[n][t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(i.Chunks)-df[t])+.5)/(float64(df[t])+.5))
			score += idf * f * 2.2 / (f + 1.2*(.25+.75*float64(lens[n])/avg))
		}
		if score > 0 {
			hits = append(hits, Hit{c, score})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].Score == hits[b].Score {
			return hits[a].ID < hits[b].ID
		}
		return hits[a].Score > hits[b].Score
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}
