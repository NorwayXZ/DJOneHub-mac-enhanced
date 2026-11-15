package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// serviceStop closes when the Windows service manager asks DJOneHub to stop,
// which a service cannot learn from a signal.
var (
	serviceStop     = make(chan struct{})
	serviceStopOnce sync.Once
)

func requestServiceStop() { serviceStopOnce.Do(func() { close(serviceStop) }) }

// shutdownContext ends on Ctrl-C, SIGTERM or a service stop request.
func shutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-serviceStop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
