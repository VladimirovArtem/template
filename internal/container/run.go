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
	if c.positions != nil {
		if err := c.positions.Start(context.WithoutCancel(ctx)); err != nil {
			_ = listener.Close()
			return fmt.Errorf("start position worker: %w", err)
		}
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
	c.shutdownOnce.Do(func() {
		c.shutdownDone = make(chan struct{})
		c.shutdownContext = ctx
		go func() {
			c.shutdownErr = c.stop(ctx)
			if ctx.Err() != nil {
				c.shutdownErr = errors.Join(c.shutdownErr, ctx.Err())
			}
			close(c.shutdownDone)
		}()
	})
	select {
	case <-c.shutdownDone:
		return c.shutdownResult()
	default:
	}
	select {
	case <-ctx.Done():
		if c.positions != nil {
			c.positions.Abort()
		}
		return ErrShutdownTimeout
	case <-c.shutdownContext.Done():
		if c.positions != nil {
			c.positions.Abort()
		}
		return ErrShutdownTimeout
	case <-c.shutdownDone:
		return c.shutdownResult()
	}
}

func (c *Container) shutdownResult() error {
	err := c.shutdownErr
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrShutdownTimeout
	}
	if err != nil {
		return fmt.Errorf("shutdown service: %w", err)
	}
	return nil
}

func (c *Container) stop(ctx context.Context) error {
	err := c.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, c.server.Close())
	}
	if c.positions != nil {
		err = errors.Join(err, c.positions.Shutdown(ctx))
		if ctx.Err() != nil {
			c.positions.Abort()
		}
		<-c.positions.Done()
	}
	if c.telemetry != nil {
		err = errors.Join(err, c.telemetry.Shutdown(ctx))
	}
	if c.push != nil {
		c.push.CloseIdleConnections()
	}
	if c.pool != nil {
		c.pool.Close()
	}
	return err
}
