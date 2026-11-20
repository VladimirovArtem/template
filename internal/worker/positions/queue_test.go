package positions

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/model"
)

func queuePut(q *queue, trip model.StaleTrip) PutResult { return q.put(trip, trace.SpanContext{}) }

func testTrip(id byte, at time.Time) model.StaleTrip {
	tripID := uuid.UUID{6: 0x40, 8: 0x80, 15: id}
	driverID := tripID
	driverID[0] = 1
	return model.StaleTrip{ID: tripID, DriverID: driverID, ReferenceAt: at}
}

func TestQueuePriority(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, equalTimes := range []bool{false, true} {
		t.Run(map[bool]string{false: "different times", true: "equal times"}[equalTimes], func(t *testing.T) {
			t.Parallel()
			q := newQueue(3)
			for _, id := range []byte{3, 1, 2} {
				at := at
				if !equalTimes {
					at = at.Add(time.Duration(id) * time.Second)
				}
				if result := queuePut(q, testTrip(id, at)); result.Status != Queued || result.Evicted != nil {
					t.Fatalf("Put: %+v", result)
				}
			}
			q.close()
			for _, id := range []byte{1, 2, 3} {
				trip, ok := q.take()
				if !ok || trip.ID != testTrip(id, at).ID || len(q.inFlight) != 1 {
					t.Fatalf("take: %+v, %t", trip, ok)
				}
				q.complete(trip.ID)
			}
			if _, ok := q.take(); ok || len(q.waiting) != 0 || len(q.inFlight) != 0 {
				t.Fatal("drained queue still has work")
			}
		})
	}
}

func TestQueueEvictionAndDuplicate(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q := newQueue(3)
	old, middle, newest := testTrip(1, at), testTrip(2, at.Add(time.Second)), testTrip(3, at.Add(2*time.Second))
	for _, trip := range []model.StaleTrip{middle, newest, old} {
		queuePut(q, trip)
	}
	changed := old
	changed.ReferenceAt, changed.DriverID = at.Add(-time.Hour), uuid.New()
	if result := queuePut(q, changed); result.Status != Duplicate || result.Evicted != nil || q.waiting[old.ID].trip != old {
		t.Fatalf("duplicate changed queue: %+v", result)
	}
	if result := queuePut(q, testTrip(4, at.Add(3*time.Second))); result.Status != Dropped || result.Evicted != nil || len(q.pending) != 3 {
		t.Fatalf("fresh task was not dropped: %+v", result)
	}
	older := testTrip(5, at.Add(-time.Second))
	result := queuePut(q, older)
	if result.Status != Queued || result.Evicted == nil || *result.Evicted != newest || len(q.pending) != 3 {
		t.Fatalf("wrong eviction: %+v", result)
	}
	if q.cancel(newest.ID) {
		t.Fatal("evicted trip is still waiting")
	}
	q.close()
	for _, want := range []model.StaleTrip{older, old, middle} {
		got, ok := q.take()
		if !ok || got.StaleTrip != want {
			t.Fatalf("take after eviction: %+v, want %+v", got, want)
		}
		q.complete(got.ID)
	}
}

func TestQueueEvictionBreaksTimeTiesByID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q := newQueue(2)
	queuePut(q, testTrip(3, at))
	queuePut(q, testTrip(1, at))
	result := queuePut(q, testTrip(2, at))
	if result.Status != Queued || result.Evicted == nil || result.Evicted.ID != testTrip(3, at).ID {
		t.Fatalf("tie eviction: %+v", result)
	}
	if result := queuePut(q, testTrip(4, at)); result.Status != Dropped {
		t.Fatalf("tie drop: %+v", result)
	}
	q.close()
	for _, id := range []byte{1, 2} {
		got, ok := q.take()
		if !ok || got.ID != testTrip(id, at).ID {
			t.Fatalf("tie order: %+v", got)
		}
		q.complete(got.ID)
	}
}

func TestQueueCancelAndInFlight(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q := newQueue(3)
	for _, id := range []byte{4, 1, 3} {
		queuePut(q, testTrip(id, at))
	}
	if !q.cancel(testTrip(3, at).ID) || q.cancel(testTrip(3, at).ID) {
		t.Fatal("Cancel did not remove exactly one waiting task")
	}
	if result := queuePut(q, testTrip(5, at)); result.Status != Queued || result.Evicted != nil {
		t.Fatalf("Cancel did not free capacity: %+v", result)
	}
	active, ok := q.take()
	if !ok || active.ID != testTrip(1, at).ID || len(q.inFlight) != 1 || len(q.pending) != 2 {
		t.Fatalf("waiting/in-flight transition: %+v", active)
	}
	if q.cancel(active.ID) || queuePut(q, active.StaleTrip).Status != Duplicate {
		t.Fatal("in-flight task lost deduplication")
	}
	if result := queuePut(q, testTrip(6, at)); result.Status != Queued || result.Evicted != nil {
		t.Fatalf("in-flight task consumed waiting capacity: %+v", result)
	}
	q.complete(active.ID)
	q.cancel(testTrip(6, at).ID)
	if result := queuePut(q, active.StaleTrip); result.Status != Queued || result.Evicted != nil {
		t.Fatalf("completed task was not released: %+v", result)
	}
	q.stop()
	if _, ok := q.take(); ok || len(q.waiting) != 0 || len(q.pending) != 0 {
		t.Fatal("forced stop kept waiting tasks")
	}
	if result := queuePut(q, active.StaleTrip); result.Status != Closed {
		t.Fatalf("closed queue looks full: %+v", result)
	}
}

func TestQueueStopKeepsInFlightUntilCompletion(t *testing.T) {
	t.Parallel()
	q := newQueue(1)
	trip := testTrip(1, time.Now())
	queuePut(q, trip)
	q.take()
	queuePut(q, testTrip(2, trip.ReferenceAt))
	q.stop()
	if len(q.pending) != 0 || len(q.waiting) != 0 || len(q.inFlight) != 1 || q.cancel(trip.ID) {
		t.Fatal("forced stop did not preserve in-flight ownership")
	}
	q.complete(trip.ID)
	if len(q.inFlight) != 0 {
		t.Fatal("in-flight task was not released")
	}
}
