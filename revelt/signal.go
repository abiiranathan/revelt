package revelt

import (
	"context"
	"encoding/json"
	"io"
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

// jsonEncode writes v as JSON to w. Extracted here so encoding/json is
// imported once rather than in every file that needs it.
func jsonEncode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
