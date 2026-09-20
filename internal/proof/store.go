// Package proof records shadow evidence, judges it, and turns it into decisions with stated uncertainty.
package proof

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type ShadowRecord struct {
	ID                                                                    string
	TS                                                                    time.Time
	TenantID, Route                                                       string
	Threshold, Similarity                                                 float64
	Query, CandidateQuery, CandidateAnswer, ActualAnswer, CandidateSource string
}

type CacheLabel struct {
	ShadowID           string
	TS                 time.Time
	Rater, RaterID     string
	Acceptable         bool
	Reason             string
	JudgePromptVersion string
}

type RoutingShadow struct {
	ID                                                    string
	TS                                                    time.Time
	TenantID, Route, Decision, CheapTarget, StrongTarget string
	Prompt, CheapAnswer, StrongAnswer                     string
	CheapScore, StrongScore                               float64
	JudgeModel, JudgePromptVersion                        string
}

type RoutingDecision struct {
	TS                                            time.Time
	RequestID, TenantID, Route, Decision, Reason string
	Score                                         float64
	CostMicros, CounterfactualMicros              int64
}

type LabeledPoint struct {
	Similarity float64
	Acceptable bool
	Rater      string
}

type CH struct{ conn driver.Conn }

func NewCH(conn driver.Conn) *CH { return &CH{conn: conn} }

func u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func (c *CH) insert(ctx context.Context, table string, n int, row func(i int) []any) error {
	b, err := c.conn.PrepareBatch(ctx, "INSERT INTO "+table)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if err := b.Append(row(i)...); err != nil {
			return err
		}
	}
	return b.Send()
}

func (c *CH) InsertShadow(ctx context.Context, rs []ShadowRecord) error {
	return c.insert(ctx, "cache_shadow", len(rs), func(i int) []any {
		r := rs[i]
		return []any{r.ID, r.TS, r.TenantID, r.Route, float32(r.Threshold), float32(r.Similarity), r.Query,
			r.CandidateQuery, r.CandidateAnswer, r.ActualAnswer, r.CandidateSource}
	})
}

func (c *CH) InsertLabels(ctx context.Context, ls []CacheLabel) error {
	return c.insert(ctx, "cache_labels", len(ls), func(i int) []any {
		l := ls[i]
		return []any{l.ShadowID, l.TS, l.Rater, l.RaterID, u8(l.Acceptable), l.Reason, l.JudgePromptVersion}
	})
}

func (c *CH) InsertRoutingShadow(ctx context.Context, rs []RoutingShadow) error {
	return c.insert(ctx, "routing_shadow", len(rs), func(i int) []any {
		r := rs[i]
		return []any{r.ID, r.TS, r.TenantID, r.Route, r.Decision, r.CheapTarget, r.StrongTarget, r.Prompt,
			r.CheapAnswer, r.StrongAnswer, float32(r.CheapScore), float32(r.StrongScore), r.JudgeModel, r.JudgePromptVersion}
	})
}

func (c *CH) InsertDecisions(ctx context.Context, ds []RoutingDecision) error {
	return c.insert(ctx, "routing_decisions", len(ds), func(i int) []any {
		d := ds[i]
		return []any{d.TS, d.RequestID, d.TenantID, d.Route, d.Decision, d.Reason, float32(d.Score), d.CostMicros, d.CounterfactualMicros}
	})
}

// UnlabeledShadow returns shadow rows in [lo, hi) similarity with no judge label yet, newest first.
func (c *CH) UnlabeledShadow(ctx context.Context, route string, lo, hi float64, limit int) ([]ShadowRecord, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT id, ts, tenant_id, route, threshold, similarity, query, candidate_query, candidate_answer, actual_answer, candidate_source
		FROM cache_shadow
		WHERE route = ? AND similarity >= ? AND similarity < ?
		  AND id NOT IN (SELECT shadow_id FROM cache_labels FINAL WHERE rater = 'judge')
		ORDER BY ts DESC LIMIT ?`, route, float32(lo), float32(hi), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShadowRecord
	for rows.Next() {
		var r ShadowRecord
		var th, sim float32
		if err := rows.Scan(&r.ID, &r.TS, &r.TenantID, &r.Route, &th, &sim, &r.Query, &r.CandidateQuery,
			&r.CandidateAnswer, &r.ActualAnswer, &r.CandidateSource); err != nil {
			return nil, err
		}
		r.Threshold, r.Similarity = float64(th), float64(sim)
		out = append(out, r)
	}
	return out, rows.Err()
}

// CurveInputs returns every lookup similarity since t (the hit-rate denominator) and every labelled point.
func (c *CH) CurveInputs(ctx context.Context, route string, since time.Time) ([]float64, []LabeledPoint, error) {
	rows, err := c.conn.Query(ctx, `SELECT similarity FROM cache_shadow WHERE route = ? AND ts >= ?`, route, since)
	if err != nil {
		return nil, nil, err
	}
	var sims []float64
	for rows.Next() {
		var s float32
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, nil, err
		}
		sims = append(sims, float64(s))
	}
	rows.Close()
	// One point per shadow row. A human label wins over the judge label for the same row.
	lrows, err := c.conn.Query(ctx, `
		SELECT s.similarity,
		       if(countIf(l.rater = 'human') > 0, anyIf(l.acceptable, l.rater = 'human'), anyIf(l.acceptable, l.rater = 'judge')) AS ok,
		       if(countIf(l.rater = 'human') > 0, 'human', 'judge') AS rater
		FROM cache_labels AS l FINAL
		INNER JOIN cache_shadow AS s ON s.id = l.shadow_id
		WHERE s.route = ? AND s.ts >= ?
		GROUP BY s.id, s.similarity`, route, since)
	if err != nil {
		return nil, nil, err
	}
	defer lrows.Close()
	var labeled []LabeledPoint
	for lrows.Next() {
		var s float32
		var ok uint8
		var p LabeledPoint
		if err := lrows.Scan(&s, &ok, &p.Rater); err != nil {
			return nil, nil, err
		}
		p.Similarity, p.Acceptable = float64(s), ok == 1
		labeled = append(labeled, p)
	}
	return sims, labeled, lrows.Err()
}

// HumanAndJudge returns aligned judge and human labels for rows that have both.
func (c *CH) HumanAndJudge(ctx context.Context, route string) ([]bool, []bool, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT j.acceptable, h.acceptable
		FROM (SELECT shadow_id, acceptable FROM cache_labels FINAL WHERE rater = 'judge') AS j
		INNER JOIN (SELECT shadow_id, acceptable FROM cache_labels FINAL WHERE rater = 'human') AS h USING shadow_id
		INNER JOIN cache_shadow AS s ON s.id = j.shadow_id
		WHERE s.route = ?`, route)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var judge, human []bool
	for rows.Next() {
		var a, b uint8
		if err := rows.Scan(&a, &b); err != nil {
			return nil, nil, err
		}
		judge, human = append(judge, a == 1), append(human, b == 1)
	}
	return judge, human, rows.Err()
}

// RoutingDeltas returns cheap_score - strong_score for shadow pairs where the live decision was cheap.
func (c *CH) RoutingDeltas(ctx context.Context, route string, since time.Time) ([]float64, error) {
	rows, err := c.conn.Query(ctx, `SELECT cheap_score - strong_score FROM routing_shadow
		WHERE route = ? AND decision = 'cheap' AND ts >= ?`, route, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var d float64
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RoutingSavings sums actual and counterfactual (all-strong) cost for smart-routed requests since t.
func (c *CH) RoutingSavings(ctx context.Context, route string, since time.Time) (actual, counterfactual int64, cheapShare float64, n int, err error) {
	var cnt uint64
	var cheap uint64
	err = c.conn.QueryRow(ctx, `SELECT sum(cost_micros), sum(counterfactual_micros), countIf(decision = 'cheap'), count()
		FROM routing_decisions WHERE route = ? AND ts >= ?`, route, since).Scan(&actual, &counterfactual, &cheap, &cnt)
	if cnt > 0 {
		cheapShare = float64(cheap) / float64(cnt)
	}
	return actual, counterfactual, cheapShare, int(cnt), err
}
