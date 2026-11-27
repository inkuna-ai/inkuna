package app

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

// How the delivery loops pace themselves. Tests shorten them.
var (
	// sinkTimeout bounds one Record call. A sink that takes longer is
	// treated as having refused the record, which is retried.
	sinkTimeout = time.Minute
	// sinkRetryMin and sinkRetryMax bound the wait before a refused record
	// is offered again; it doubles with each refusal in a row.
	sinkRetryMin = time.Second
	sinkRetryMax = 5 * time.Minute
	// outboxPoll is how often an idle loop looks for records it was not
	// woken for, such as ones a backfill queued.
	outboxPoll = 30 * time.Second
)

// outboxBatch is how many records a loop reads at a time.
const outboxBatch = 100

// outboxDelivery runs one delivery loop per registered sink.
type outboxDelivery struct {
	installation string
	sinks        []*sinkDelivery
	cancel       context.CancelFunc
	done         sync.WaitGroup
}

// sinkDelivery is one sink's loop: the records of its kind, in order, each
// offered until the sink accepts it.
type sinkDelivery struct {
	registeredSink
	kind storage.OutboxKind
	// wake is signalled, without blocking, whenever a record of the sink's
	// kind is queued.
	wake chan struct{}
	// pending is the result of a Record call that outlived sinkTimeout and
	// is still running. Only the sink's loop touches it.
	pending chan error
}

// startSinks turns the outbox on for the kinds this build has sinks for,
// queues the usage already stored the first time a data directory has a
// usage sink, and starts a delivery loop per sink. A build with no sink
// writes nothing to the outbox and starts nothing.
func (service *Service) startSinks() error {
	registry := service.extensions
	if service.outbox != nil || service.store == nil || registry == nil || len(registry.sinks) == 0 {
		return nil
	}
	ctx := context.Background()
	store := service.store
	installation, err := store.Installation(ctx)
	if err != nil {
		return err
	}
	delivery := &outboxDelivery{installation: installation}
	var kinds []storage.OutboxKind
	cursors := map[string]int64{}
	for _, registered := range registry.sinks {
		sink := &sinkDelivery{registeredSink: registered, kind: storage.OutboxAudit, wake: make(chan struct{}, 1)}
		if registered.usage != nil {
			sink.kind = storage.OutboxUsage
		}
		state, err := store.RegisterOutboxSink(ctx, sink.name, sink.kind)
		if err != nil {
			return err
		}
		cursors[sink.name] = state.DeliveredThrough
		kinds = append(kinds, sink.kind)
		delivery.sinks = append(delivery.sinks, sink)
	}
	store.EnableOutbox(delivery.wake, kinds...)
	for _, kind := range kinds {
		if kind != storage.OutboxUsage {
			continue
		}
		// After the outbox is on, so a call recorded meanwhile is queued by
		// RecordUsage and skipped here.
		if _, err := store.BackfillUsage(ctx); err != nil {
			return err
		}
		break
	}
	loopContext, cancel := context.WithCancel(ctx)
	delivery.cancel = cancel
	for _, sink := range delivery.sinks {
		delivery.done.Add(1)
		go func() {
			defer delivery.done.Done()
			delivery.run(loopContext, store, sink, cursors[sink.name])
		}()
	}
	service.outbox = delivery
	return nil
}

// stopSinks stops the delivery loops and waits for them. A record a sink
// is handling is offered again at the next start.
func (service *Service) stopSinks() {
	if service.outbox == nil {
		return
	}
	service.outbox.cancel()
	service.outbox.done.Wait()
}

func (delivery *outboxDelivery) wake(kind storage.OutboxKind) {
	for _, sink := range delivery.sinks {
		if sink.kind != kind {
			continue
		}
		select {
		case sink.wake <- struct{}{}:
		default:
		}
	}
}

// run gives a sink every record of its kind after through, in order. A
// record the sink refuses is offered again, after a wait that doubles up to
// sinkRetryMax, and nothing after it is offered until it is accepted.
func (delivery *outboxDelivery) run(ctx context.Context, store *storage.Store, sink *sinkDelivery, through int64) {
	progress := sinkProgress{store: store, sink: sink.name, through: through, saved: through}
	for ctx.Err() == nil {
		records, err := store.Outbox(ctx, sink.kind, progress.through, outboxBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("inkuna: sink %s: %v", sink.name, err)
			}
			if !sleep(ctx, sinkRetryMax, nil) {
				return
			}
			continue
		}
		if len(records) == 0 {
			// A batch whose progress could not be saved is saved now, or
			// Doctor would show it waiting until another record came.
			progress.save()
			if !sleep(ctx, outboxPoll, sink.wake) {
				return
			}
			continue
		}
		for _, record := range records {
			if !delivery.offer(ctx, sink, record, &progress) {
				return
			}
		}
		progress.save()
	}
}

// sinkProgress is how far a sink has got: through in memory, saved in the
// store. It is saved once a batch is delivered, and before a refused record
// waits, rather than after every record, which would double the writes.
type sinkProgress struct {
	store          *storage.Store
	sink           string
	through, saved int64
}

func (progress *sinkProgress) save() {
	if progress.through == progress.saved {
		return
	}
	// Saved even as the loop stops: the sink accepted those records.
	if err := progress.store.MarkOutboxDelivered(context.Background(), progress.sink, progress.through); err != nil {
		log.Printf("inkuna: %v", err)
		return
	}
	progress.saved = progress.through
}

// offer gives one record to a sink until it accepts it. Each refusal is
// recorded, after what the sink accepted before it, so Doctor shows a stuck
// sink as it stands. It returns false once the loop is stopping.
func (delivery *outboxDelivery) offer(ctx context.Context, sink *sinkDelivery, record storage.OutboxRecord, progress *sinkProgress) bool {
	wait := sinkRetryMin
	for {
		err := delivery.record(ctx, sink, record)
		if err == nil {
			progress.through = record.ID
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		progress.save()
		if markErr := progress.store.MarkOutboxFailed(context.Background(), sink.name, err.Error()); markErr != nil {
			log.Printf("inkuna: %v", markErr)
		}
		if !sleep(ctx, wait, nil) {
			return false
		}
		wait = min(wait*2, sinkRetryMax)
	}
}

// record hands one record to its sink, bounded by sinkTimeout. The sink runs
// apart, so one that ignores its context or panics still cannot stop the
// loop: either counts as a refusal. A sink has at most one call running: a
// call still running after its timeout is waited for again on the next
// attempt, which is for the same record, rather than joined by another, so
// a hung sink holds one goroutine, not one per retry.
func (delivery *outboxDelivery) record(ctx context.Context, sink *sinkDelivery, record storage.OutboxRecord) error {
	result := sink.pending
	if result == nil {
		result = delivery.call(ctx, sink, record)
	}
	timer := time.NewTimer(sinkTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		sink.pending = nil
		return err
	case <-timer.C:
		sink.pending = result
		return fmt.Errorf("sink %s did not accept record %d within %s", sink.name, record.ID, sinkTimeout)
	case <-ctx.Done():
		sink.pending = result
		return ctx.Err()
	}
}

// call starts one Record call, whose context ends after sinkTimeout, and
// returns where its result will arrive.
func (delivery *outboxDelivery) call(ctx context.Context, sink *sinkDelivery, record storage.OutboxRecord) chan error {
	ctx, cancel := context.WithTimeout(ctx, sinkTimeout)
	result := make(chan error, 1)
	go func() {
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				result <- fmt.Errorf("sink %s panicked: %v", sink.name, recovered)
			}
		}()
		switch {
		case sink.audit != nil && record.Audit != nil:
			result <- sink.audit.Record(ctx, auditEvent(delivery.installation, record))
		case sink.usage != nil && record.Usage != nil:
			result <- sink.usage.Record(ctx, UsageRecordOf(delivery.installation, *record.Usage))
		default:
			result <- fmt.Errorf("outbox record %d is not a %s record", record.ID, sink.kind)
		}
	}()
	return result
}

// sleep waits for d, or until wake is signalled, and reports false when ctx
// ended first.
func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-wake:
	}
	return true
}

// auditEvent is a queued audit record as a sink receives it. Its ID comes
// from the outbox row, so it is the same at every redelivery.
func auditEvent(installation string, record storage.OutboxRecord) extension.AuditEvent {
	audit := record.Audit
	return extension.AuditEvent{
		ID: installation + "/audit/" + strconv.FormatInt(record.ID, 10), Time: audit.Time, Kind: audit.Kind, Installation: installation,
		Project: audit.Project, Task: audit.Task, Run: audit.Run, Actor: audit.Actor, Principal: audit.Principal, Summary: audit.Summary,
		Fields: maps.Clone(audit.Fields),
	}
}

// UsageRecordOf is a queued usage row as a sink receives it and
// `inkunactl usage export` writes it. Its ID comes from the usage row, so it
// is the same at every redelivery, and the same whether the row was queued
// as it was recorded, by the backfill, or read for an export.
func UsageRecordOf(installation string, queued storage.QueuedUsage) extension.UsageRecord {
	call := queued.Record
	return extension.UsageRecord{
		ID: installation + "/" + strconv.FormatInt(call.ID, 10), Installation: installation, Principal: queued.Principal, Project: queued.Project,
		Task: call.TaskID, Run: call.ExecutionID, Step: call.StepID, Call: call.CallID, Turn: call.Turn, Kind: call.Kind, Agent: call.Agent,
		Runtime: string(call.Runtime), Model: call.Model, ModelUsed: call.ModelUsed, AuthMode: call.AuthMode, Outcome: call.Outcome,
		StartedAt: call.StartedAt, FinishedAt: call.FinishedAt,
		Tokens: extension.Tokens{
			Input: call.Usage.InputTokens, CacheWrite: call.Usage.CacheWriteTokens, LongCacheWrite: call.Usage.LongCacheWriteTokens,
			CacheRead: call.Usage.CacheReadTokens, Output: call.Usage.OutputTokens,
		},
		Premium: call.Usage.PremiumRequests,
		Cost:    extension.Cost{Source: call.Cost.Source, Cents: call.Cost.Cents, EstimatedCents: call.Cost.EstimatedCents, PricingVersion: call.Cost.PricingVersion},
	}
}

// audit queues an audit event for the audit sinks. It does nothing in a
// build without one. The decision it records has already been made, so a
// queue that fails is logged, not returned.
func (service *Service) audit(record storage.AuditRecord) {
	if service.store == nil || !service.store.Auditing() {
		return
	}
	if err := service.store.QueueAudit(context.Background(), record); err != nil {
		log.Printf("inkuna: %v", err)
	}
}

// auditing reports whether audit events are queued, so a caller can skip
// the work of building one.
func (service *Service) auditing() bool {
	return service.store != nil && service.store.Auditing()
}

// sinkHealth is one Doctor row per sink this data directory has had: how
// many records wait for it, and what it last returned when it refused one.
func (service *Service) sinkHealth() []HealthCheck {
	if service.store == nil {
		return nil
	}
	sinks, err := service.store.OutboxSinks(service.context())
	if err != nil {
		return []HealthCheck{{Name: "Sinks", Status: "error", Detail: err.Error()}}
	}
	registered := map[string]bool{}
	if service.extensions != nil {
		for _, sink := range service.extensions.sinks {
			registered[sink.name] = true
		}
	}
	checks := make([]HealthCheck, 0, len(sinks))
	for _, sink := range sinks {
		check := HealthCheck{Name: "Sink " + sink.Name, Status: "ready"}
		switch sink.Backlog {
		case 0:
			check.Detail = "every " + string(sink.Kind) + " record delivered"
		case 1:
			check.Detail = "1 " + string(sink.Kind) + " record waiting"
		default:
			check.Detail = fmt.Sprintf("%d %s records waiting", sink.Backlog, sink.Kind)
		}
		if !registered[sink.Name] {
			check.Status = "missing"
			check.Detail += "; not registered in this build"
		}
		if sink.LastError != "" {
			// A sink the build lacks stays missing: that is what to fix.
			if registered[sink.Name] {
				check.Status = "error"
			}
			check.Detail += fmt.Sprintf("; refused %d times in a row, last at %s: %s", sink.Failures, sink.FailedAt.Local().Format(time.DateTime), sink.LastError)
		}
		checks = append(checks, check)
	}
	return checks
}
