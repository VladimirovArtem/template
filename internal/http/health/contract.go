//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package health_test

package health

import "context"

type pinger interface {
	Ping(context.Context) error
}
