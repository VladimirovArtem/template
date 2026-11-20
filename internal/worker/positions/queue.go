package positions

import (
	"container/heap"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/model"
)

type PutStatus string

const (
	Queued    PutStatus = "queued"
	Duplicate PutStatus = "duplicate"
	Dropped   PutStatus = "dropped"
	Closed    PutStatus = "closed"
)

type PutResult struct {
	Status  PutStatus
	Evicted *model.StaleTrip
}

type queue struct {
	mu       sync.Mutex
	ready    *sync.Cond
	capacity int
	pending  tripHeap
	waiting  map[uuid.UUID]*entry
	inFlight map[uuid.UUID]struct{}
	closed   bool
	stopped  bool
}

func newQueue(capacity int) *queue {
	q := &queue{
		capacity: capacity, pending: make(tripHeap, 0, capacity),
		waiting: make(map[uuid.UUID]*entry, capacity), inFlight: make(map[uuid.UUID]struct{}),
	}
	q.ready = sync.NewCond(&q.mu)
	return q
}

func (q *queue) put(trip model.StaleTrip, check trace.SpanContext) PutResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return PutResult{Status: Closed}
	}
	if _, exists := q.waiting[trip.ID]; exists {
		return PutResult{Status: Duplicate}
	}
	if _, exists := q.inFlight[trip.ID]; exists {
		return PutResult{Status: Duplicate}
	}
	result := PutResult{Status: Queued}
	if len(q.pending) == q.capacity {
		newest := 0
		for i := 1; i < len(q.pending); i++ {
			if earlier(q.pending[newest].trip, q.pending[i].trip) {
				newest = i
			}
		}
		if !earlier(trip, q.pending[newest].trip) {
			return PutResult{Status: Dropped}
		}
		evicted := heap.Remove(&q.pending, newest).(*entry).trip
		delete(q.waiting, evicted.ID)
		result.Evicted = &evicted
	}
	item := &entry{trip: trip, check: check}
	heap.Push(&q.pending, item)
	q.waiting[trip.ID] = item
	q.ready.Signal()
	return result
}

func (q *queue) cancel(id uuid.UUID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, exists := q.waiting[id]
	if !exists {
		return false
	}
	heap.Remove(&q.pending, item.index)
	delete(q.waiting, id)
	return true
}

func (q *queue) take() (task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pending) == 0 && !q.closed {
		q.ready.Wait()
	}
	if q.stopped || len(q.pending) == 0 {
		return task{}, false
	}
	item := heap.Pop(&q.pending).(*entry)
	delete(q.waiting, item.trip.ID)
	q.inFlight[item.trip.ID] = struct{}{}
	return task{StaleTrip: item.trip, check: item.check}, true
}

func (q *queue) complete(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inFlight, id)
}

func (q *queue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.ready.Broadcast()
}

func (q *queue) stop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed, q.stopped = true, true
	q.pending = nil
	clear(q.waiting)
	q.ready.Broadcast()
}

func (q *queue) snapshot() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending), len(q.inFlight)
}
