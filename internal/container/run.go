package container

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

func (c *Container) Run(ctx context.Context) (result error) {
	defer func() {
		c.logger.Info("service stopping")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.shutdownTimeout)
		defer cancel()
		if err := c.shutdown(shutdownCtx); err != nil {
			result = errors.Join(result, err)
		} else {
			c.logger.Info("service stopped")
		}
	}()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", c.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on HTTP_ADDR: %w", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- c.server.Serve(listener) }()
	c.logger.InfoContext(ctx, "HTTP server started", "addr", listener.Addr().String())
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}

func (c *Container) shutdown(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		err := c.server.Shutdown(ctx)
		if err != nil {
			err = errors.Join(err, c.server.Close())
		}
		err = errors.Join(err, c.telemetry.Shutdown(ctx))
		if c.pool != nil {
			c.pool.Close()
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ErrShutdownTimeout
	case err := <-done:
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return ErrShutdownTimeout
		}
		if err != nil {
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		return nil
	}
}
