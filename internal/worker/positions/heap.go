package positions

import (
	"bytes"

	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/model"
)

type entry struct {
	trip  model.StaleTrip
	check trace.SpanContext
	index int
}

type task struct {
	model.StaleTrip
	check trace.SpanContext
}

type tripHeap []*entry

func (h tripHeap) Len() int           { return len(h) }
func (h tripHeap) Less(i, j int) bool { return earlier(h[i].trip, h[j].trip) }
func (h tripHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}

func (h *tripHeap) Push(value any) {
	item := value.(*entry)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *tripHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	*h = old[:last]
	item.index = -1
	return item
}

func earlier(a, b model.StaleTrip) bool {
	if !a.ReferenceAt.Equal(b.ReferenceAt) {
		return a.ReferenceAt.Before(b.ReferenceAt)
	}
	return bytes.Compare(a.ID[:], b.ID[:]) < 0
}
