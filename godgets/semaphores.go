// Copyright (c) 2018 Shivaram Lingamneni
// released under the 0BSD license

package godgets

import (
	"context"
	"time"
)

// Semaphore is a counting semaphore.
// A semaphore of capacity 1 can be used as a trylock.
type Semaphore (chan empty)

// NewSemaphore creates and initializes a semaphore to a given capacity.
func NewSemaphore(capacity int) Semaphore {
	return make(chan empty, capacity)
}

// Acquire acquires a semaphore, blocking if necessary.
func (semaphore Semaphore) Acquire() {
	semaphore <- empty{}
}

// TryAcquire tries to acquire a semaphore, returning whether the acquire was
// successful. It never blocks.
func (semaphore Semaphore) TryAcquire() (acquired bool) {
	select {
	case semaphore <- empty{}:
		return true
	default:
		return false
	}
}

// AcquireWithTimeout tries to acquire a semaphore, blocking for a maximum
// of approximately `d` while waiting for it. It returns whether the acquire
// was successful.
func (semaphore Semaphore) AcquireWithTimeout(timeout time.Duration) (acquired bool) {
	if timeout <= 0 {
		return semaphore.TryAcquire()
	}

	timer := time.NewTimer(timeout)
	select {
	case semaphore <- empty{}:
		acquired = true
	case <-timer.C:
		acquired = false
	}
	timer.Stop()
	return
}

// AcquireWithContext tries to acquire a semaphore, blocking at most until
// the context expires. It returns whether the acquire was successful.
// Note that if the context is already expired, the acquire may succeed anyway.
func (semaphore Semaphore) AcquireWithContext(ctx context.Context) (acquired bool) {
	select {
	case semaphore <- empty{}:
		acquired = true
	case <-ctx.Done():
		acquired = false
	}
	return
}

// Release releases a semaphore.
func (semaphore Semaphore) Release() {
	<-semaphore
}
