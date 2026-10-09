package proofingengine

import "testing"

// The /events long polls are capped per session and in the process; a
// released slot is free again.
func TestEventWaitersCapped(t *testing.T) {
	w := eventWaiters{bySession: map[string]int{}}
	for range maxEventWaitersPerSession {
		if !w.acquire("a") {
			t.Fatal("a waiter under the per-session cap was refused")
		}
	}
	if w.acquire("a") {
		t.Fatal("a waiter past the per-session cap was let in")
	}
	w.release("a")
	if !w.acquire("a") {
		t.Fatal("a released slot was not free again")
	}

	w = eventWaiters{bySession: map[string]int{}, total: maxEventWaiters}
	if w.acquire("b") {
		t.Fatal("a waiter past the process cap was let in")
	}
}
