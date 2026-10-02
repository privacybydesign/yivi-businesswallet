package session

import (
	"testing"
	"time"
)

func TestStatusMachineHappyPath(t *testing.T) {
	now := time.Now().UTC()
	s := Session{Status: StatusCreated}

	steps := []Status{StatusOpened, StatusInProgress, StatusNeedsReview, StatusApproved}
	for _, next := range steps {
		if err := s.SetStatus(next, now); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}
	if s.Status != StatusApproved {
		t.Fatalf("status = %s, want approved", s.Status)
	}
	if s.OpenedAt == nil {
		t.Fatal("openedAt was not stamped")
	}
	if s.CompletedAt == nil {
		t.Fatal("completedAt was not stamped on terminal status")
	}
	if !s.Status.Terminal() {
		t.Fatal("approved should be terminal")
	}
}

func TestStatusMachineRejectsInvalidTransition(t *testing.T) {
	now := time.Now().UTC()
	s := Session{Status: StatusCreated}
	// created cannot jump straight to approved; opened/in_progress must happen first.
	if err := s.SetStatus(StatusApproved, now); err == nil {
		t.Fatal("expected error transitioning created -> approved")
	}
	if s.Status != StatusCreated {
		t.Fatalf("status should be unchanged after rejected transition, got %s", s.Status)
	}
}

func TestStatusMachineTerminalIsFinal(t *testing.T) {
	now := time.Now().UTC()
	s := Session{Status: StatusRejected}
	if err := s.SetStatus(StatusInProgress, now); err == nil {
		t.Fatal("expected error transitioning out of a terminal status")
	}
}

func TestStatusMachineNoopSameStatus(t *testing.T) {
	now := time.Now().UTC()
	s := Session{Status: StatusInProgress, UpdatedAt: now.Add(-time.Hour)}
	if err := s.SetStatus(StatusInProgress, now); err != nil {
		t.Fatalf("re-setting the same status should be a no-op, got %v", err)
	}
	if s.UpdatedAt.Equal(now) {
		t.Fatal("no-op transition should not stamp UpdatedAt")
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now().UTC()
	s := Session{Status: StatusInProgress, ExpiresAt: now.Add(-time.Minute)}
	if !s.IsExpired(now) {
		t.Fatal("expected session to be expired")
	}
	s.Status = StatusApproved
	if s.IsExpired(now) {
		t.Fatal("a terminal session should never report as expired")
	}
	s.Status = StatusNeedsReview
	if s.IsExpired(now) {
		t.Fatal("a session under review waits for its decision and never expires")
	}
}

func TestFace1Vocabulary(t *testing.T) {
	// Sanity-check the reconciliation named in the issue: #1's list
	// (pending, opened, submitted, verified, rejected, expired) all map onto
	// valid states/transitions in the shared machine.
	now := time.Now().UTC()
	s := Session{Status: StatusCreated} // pending
	for _, next := range []Status{StatusOpened, StatusInProgress, StatusApproved} {
		if err := s.SetStatus(next, now); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}
}

// A reference photo is stored only while the face step can still run.
func TestForStorageDropsTheReferencePhoto(t *testing.T) {
	for status, kept := range map[Status]bool{
		StatusCreated: true, StatusOpened: true, StatusInProgress: true,
		StatusNeedsReview: false, StatusApproved: false, StatusRejected: false, StatusExpired: false, StatusCancelled: false,
	} {
		sess := Session{Status: status, ReferencePhoto: "cGhvdG8=", ReferencePhotoMime: "image/jpeg"}.ForStorage()
		if got := sess.ReferencePhoto != "" && sess.ReferencePhotoMime != ""; got != kept {
			t.Errorf("%s: photo kept = %v, want %v", status, got, kept)
		}
	}
}
