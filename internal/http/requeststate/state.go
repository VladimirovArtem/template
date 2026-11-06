package requeststate

import "context"

type key struct{}

type State struct {
	Route       string
	Status      int
	WroteHeader bool
	Operation   string
	Err         error
	Stack       []byte
}

func NewContext(ctx context.Context, state *State) context.Context {
	return context.WithValue(ctx, key{}, state)
}

func FromContext(ctx context.Context) *State {
	state, _ := ctx.Value(key{}).(*State)
	return state
}

func RecordError(ctx context.Context, operation string, err error) bool {
	state := FromContext(ctx)
	if state == nil {
		return false
	}
	state.Operation, state.Err = operation, err
	return true
}
