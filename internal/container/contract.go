//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package container

package container

import "context"

type positionWorker interface {
	Start(context.Context) error
	Shutdown(context.Context) error
	Abort()
	Done() <-chan struct{}
}

type telemetryShutdown interface {
	Shutdown(context.Context) error
}

type pushClient interface {
	CloseIdleConnections()
}

type databasePool interface {
	Close()
}
