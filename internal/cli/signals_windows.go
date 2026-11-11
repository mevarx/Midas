//go:build windows

package cli

import (
	"context"
	"os"
	"os/signal"
)

func InterruptContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}
