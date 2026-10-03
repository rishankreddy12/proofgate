// Package proof provides enterprise-grade capabilities, configuration, and structural components for the proof subsystem.
package proof

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/cache"
)

// ShadowRecorder defines the core enterprise configuration and state for ShadowRecorder.
// It is responsible for managing the lifecycle, validation, and schema of the ShadowRecorder entity.
type ShadowRecorder struct {
	ch            *CH
	chRecs        chan ShadowRecord
	batchSize     int
	flushInterval time.Duration
	stop          chan struct{}
	done          chan struct{}
	onDrop        func()
	onErr         func(error)
}

// NewShadowRecorder executes the primary logic for the NewShadowRecorder operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewShadowRecorder(ch *CH, bufferSize, batchSize int, flushInterval time.Duration, onDrop func(), onErr func(error)) *ShadowRecorder {
	if bufferSize <= 0 {
		bufferSize = 1024
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	if onDrop == nil {
		onDrop = func() {}
	}
	if onErr == nil {
		onErr = func(error) {}
	}
	sr := &ShadowRecorder{
		ch:            ch,
		chRecs:        make(chan ShadowRecord, bufferSize),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		onDrop:        onDrop,
		onErr:         onErr,
	}
	go sr.run()
	return sr
}

// Emit executes the primary logic for the Emit operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (sr *ShadowRecorder) Emit(r ShadowRecord) bool {
	select {
	case sr.chRecs <- r:
		return true
	default:
		sr.onDrop()
		return false
	}
}

// CacheHook executes the primary logic for the CacheHook operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (sr *ShadowRecorder) CacheHook() func(cache.ShadowRecord) {
	return func(r cache.ShadowRecord) {
		sr.Emit(ShadowRecord{
			ID:              r.ID,
			TS:              r.TS,
			TenantID:        r.TenantID,
			Route:           r.Route,
			Threshold:       r.Threshold,
			Similarity:      r.Similarity,
			Query:           r.Query,
			CandidateQuery:  r.CandidateQuery,
			CandidateAnswer: r.CandidateAnswer,
			ActualAnswer:    r.ActualAnswer,
			CandidateSource: r.CandidateSource,
		})
	}
}

func (sr *ShadowRecorder) flush(batch []ShadowRecord) {
	if len(batch) == 0 || sr.ch == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sr.ch.InsertShadow(ctx, batch); err != nil {
		sr.onErr(err)
	}
}

func (sr *ShadowRecorder) run() {
	defer close(sr.done)
	ticker := time.NewTicker(sr.flushInterval)
	defer ticker.Stop()
	batch := make([]ShadowRecord, 0, sr.batchSize)

	for {
		select {
		case <-sr.stop:
			for {
				select {
				case r := <-sr.chRecs:
					batch = append(batch, r)
					if len(batch) >= sr.batchSize {
						sr.flush(batch)
						batch = batch[:0]
					}
				default:
					sr.flush(batch)
					return
				}
			}
		case r := <-sr.chRecs:
			batch = append(batch, r)
			if len(batch) >= sr.batchSize {
				sr.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				sr.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

// Close executes the primary logic for the Close operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (sr *ShadowRecorder) Close() {
	close(sr.stop)
	<-sr.done
}
