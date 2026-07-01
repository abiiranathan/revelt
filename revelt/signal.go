package revelt

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalNotifyContext returns a context that is cancelled on SIGINT or SIGTERM,
// along with a stop function that releases the signal subscription. It is called
// internally by NewApp; callers never need to invoke it directly.
func signalNotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
