package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"backend/internal/poller"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSaveAndQueryMeasurements(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Minute).Truncate(time.Millisecond)

	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		err := s.Save(ctx, []poller.Measurement{
			{Source: "sensor", Metric: "temperature_c", Value: 20 + float64(i), At: at},
			{Source: "sensor", Metric: "humidity_pct", Value: 50, At: at},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Measurements(ctx, "sensor", "temperature_c", base, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5", len(got))
	}
	for i, m := range got {
		if m.Value != 20+float64(i) {
			t.Fatalf("row %d value = %v (not time ordered?)", i, m.Value)
		}
	}

	// limit returns the NEWEST rows, still in ascending order
	got, err = s.Measurements(ctx, "sensor", "temperature_c", base, 2)
	if err != nil || len(got) != 2 || got[0].Value != 23 || got[1].Value != 24 {
		t.Fatalf("limit result = %+v, err=%v", got, err)
	}
}

func TestEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, msg := range []string{"first", "second"} {
		if err := s.AddEvent(ctx, Event{Kind: "status_change", Source: "sensor", Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Events(ctx, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("events = %+v, err=%v", got, err)
	}
	if got[0].Message != "second" {
		t.Fatalf("want newest first, got %q", got[0].Message)
	}
}

// Concurrent writers and readers; meaningful under -race.
func TestConcurrentAccess(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.Save(ctx, []poller.Measurement{{Source: "s", Metric: "m", Value: 1, At: time.Now()}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.Measurements(ctx, "s", "m", time.Now().Add(-time.Hour), 10)
				_ = s.AddEvent(ctx, Event{Kind: "k", Source: "s", Message: "m"})
			}
		}()
	}
	wg.Wait()
}

func TestConfigRoundTripAndOverwrite(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	if _, found, err := s.GetConfig(ctx, "control"); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	if err := s.PutConfig(ctx, "control", `{"on_above":30}`, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutConfig(ctx, "control", `{"on_above":31}`, "bob"); err != nil {
		t.Fatal(err)
	}
	v, found, err := s.GetConfig(ctx, "control")
	if err != nil || !found || v != `{"on_above":31}` {
		t.Fatalf("got %q found=%v err=%v", v, found, err)
	}
}
