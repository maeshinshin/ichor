package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A call cut short by the wait's own deadline must not replace the last real answer.
func TestPollWhileReportsLastRealErrorOnDeadline(t *testing.T) {
	calls := 0
	err := pollWhile(context.Background(), 100*time.Millisecond, 20*time.Millisecond, io.Discard, "x",
		func(ctx context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("webhook not up")
			}
			<-ctx.Done()

			return errors.New("client rate limiter Wait returned an error: " + ctx.Err().Error())
		}, func(error) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "webhook not up") {
		t.Fatalf("err = %v, want the last real error", err)
	}
}
