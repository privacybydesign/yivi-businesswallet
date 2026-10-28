package main

import (
	"testing"
	"time"

	"github.com/klippa-app/go-pdfium"
)

// diplomaPoolWait is how long the test waits for an instance past the
// diploma pool's bound before calling it exhausted.
const diplomaPoolWait = 100 * time.Millisecond

// The diploma parser's pool holds at most diplomaPDFiumMaxTotal instances,
// none of them the VOG parser's.
func TestDiplomaPDFiumPoolIsBounded(t *testing.T) {
	pool, err := newDiplomaPDFiumPool()
	if err != nil {
		t.Fatalf("newDiplomaPDFiumPool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close pool: %v", err)
		}
	})
	held := make([]pdfium.Pdfium, 0, diplomaPDFiumMaxTotal)
	t.Cleanup(func() {
		for _, instance := range held {
			if err := instance.Close(); err != nil {
				t.Errorf("return instance: %v", err)
			}
		}
	})
	for range diplomaPDFiumMaxTotal {
		instance, err := pool.GetInstance(diplomaPoolWait)
		if err != nil {
			t.Fatalf("GetInstance within the bound: %v", err)
		}
		held = append(held, instance)
	}
	if instance, err := pool.GetInstance(diplomaPoolWait); err == nil {
		held = append(held, instance)
		t.Errorf("GetInstance past diplomaPDFiumMaxTotal succeeded, want the pool exhausted")
	}
}
