package uiapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

func TestLoginsCannotStarvePasswordChangesAndQueueIsBounded(t *testing.T) {
	u := NewUserSettings(nil, "https://i")
	ctx := context.Background()

	hold, err := u.acquireLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The signed-in operations have their own slot.
	ctxShort, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	rel, err := u.acquire(ctxShort)
	if err != nil {
		t.Fatalf("a busy login slot blocked a password operation: %v", err)
	}

	rel()

	// maxLoginWaiters more may queue; the next one is refused at once.
	queued := make(chan error, maxLoginWaiters)
	qctx, qcancel := context.WithCancel(ctx)

	for range maxLoginWaiters {
		go func() { _, err := u.acquireLogin(qctx); queued <- err }()
	}

	deadline := time.Now().Add(5 * time.Second)
	for u.waiting.Load() < maxLoginWaiters+hashSlotsLogin && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := u.acquireLogin(ctx); !errors.Is(err, oidcissuer.ErrBusy) {
		t.Fatalf("an over-full login queue: %v", err)
	}

	qcancel()
	hold()

	for range maxLoginWaiters {
		if err := <-queued; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("queued login: %v", err)
		}
	}
}
