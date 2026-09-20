package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/proof"
)

func runLabelCache(ctx context.Context, route string, limit int, chURL string) {
	if chURL == "" {
		chURL = os.Getenv("CLICKHOUSE_URL")
	}
	if chURL == "" {
		chURL = "clickhouse://pg:pg@127.0.0.1:19000/proofgate"
	}
	conn, err := analytics.Open(ctx, chURL)
	if err != nil {
		die("clickhouse connect: %v", err)
	}
	defer conn.Close()

	ch := proof.NewCH(conn)

	query := `
		SELECT id, ts, tenant_id, route, threshold, similarity, query, candidate_query, candidate_answer, actual_answer, candidate_source
		FROM cache_shadow
		WHERE (route = ? OR ? = '')
		  AND id NOT IN (SELECT shadow_id FROM cache_labels FINAL WHERE rater = 'human')
		ORDER BY ts DESC LIMIT ?`

	rows, err := conn.Query(ctx, query, route, route, limit)
	if err != nil {
		die("query cache_shadow: %v", err)
	}
	defer rows.Close()

	var records []proof.ShadowRecord
	for rows.Next() {
		var r proof.ShadowRecord
		var th, sim float32
		if err := rows.Scan(&r.ID, &r.TS, &r.TenantID, &r.Route, &th, &sim, &r.Query,
			&r.CandidateQuery, &r.CandidateAnswer, &r.ActualAnswer, &r.CandidateSource); err != nil {
			die("scan shadow record: %v", err)
		}
		r.Threshold = float64(th)
		r.Similarity = float64(sim)
		records = append(records, r)
	}

	if len(records) == 0 {
		fmt.Println("No unlabeled cache shadow records found.")
		return
	}

	raterID := os.Getenv("USER")
	if raterID == "" {
		raterID = os.Getenv("USERNAME")
	}
	if raterID == "" {
		raterID = "human"
	}

	reader := bufio.NewReader(os.Stdin)
	labeledCount := 0

	for i, r := range records {
		fmt.Printf("\n[%d/%d] Route: %s | Similarity: %.4f | Source: %s\n", i+1, len(records), r.Route, r.Similarity, r.CandidateSource)
		fmt.Printf("Query:\n  %s\n", r.Query)
		fmt.Printf("Candidate Cached Answer (from: %q):\n  %s\n", r.CandidateQuery, r.CandidateAnswer)
		fmt.Printf("Fresh Model Answer:\n  %s\n", r.ActualAnswer)

		for {
			fmt.Print("Acceptable? [y/n/skip/quit]: ")
			input, _ := reader.ReadString('\n')
			input = strings.ToLower(strings.TrimSpace(input))

			if input == "quit" || input == "q" {
				fmt.Printf("Done. Labeled %d records.\n", labeledCount)
				return
			}
			if input == "skip" || input == "s" {
				break
			}
			if input == "y" || input == "yes" {
				lbl := proof.CacheLabel{
					ShadowID:   r.ID,
					TS:         time.Now().UTC(),
					Rater:      "human",
					RaterID:    raterID,
					Acceptable: true,
					Reason:     "",
				}
				if err := ch.InsertLabels(ctx, []proof.CacheLabel{lbl}); err != nil {
					fmt.Printf("Error inserting label: %v\n", err)
				} else {
					labeledCount++
					fmt.Println("Recorded: acceptable=true")
				}
				break
			}
			if input == "n" || input == "no" {
				fmt.Print("Reason (optional): ")
				reason, _ := reader.ReadString('\n')
				reason = strings.TrimSpace(reason)

				lbl := proof.CacheLabel{
					ShadowID:   r.ID,
					TS:         time.Now().UTC(),
					Rater:      "human",
					RaterID:    raterID,
					Acceptable: false,
					Reason:     reason,
				}
				if err := ch.InsertLabels(ctx, []proof.CacheLabel{lbl}); err != nil {
					fmt.Printf("Error inserting label: %v\n", err)
				} else {
					labeledCount++
					fmt.Println("Recorded: acceptable=false")
				}
				break
			}
			fmt.Println("Invalid input. Please enter y, n, skip, or quit.")
		}
	}
	fmt.Printf("\nFinished reviewing. Total labeled: %d\n", labeledCount)
}
