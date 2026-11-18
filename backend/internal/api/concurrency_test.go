package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/control"
)

type configStore struct {
	once    sync.Once
	before  func()
	current string
}

func (s *configStore) GetConfig(context.Context, string) (string, bool, error) {
	return s.current, s.current != "", nil
}

func (s *configStore) PutConfig(_ context.Context, _, value, _ string) error {
	if s.before != nil {
		s.once.Do(s.before)
	}
	s.current = value
	return nil
}

type startedBody struct {
	io.Reader
	once    sync.Once
	started chan struct{}
}

func (b *startedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.Reader.Read(p)
}

func await[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach the expected barrier")
		var zero T
		return zero
	}
}

func requestAsync(server http.Handler, method, path, token string, body io.Reader) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

func TestConcurrentControlHTTPPatchesPreserveBothFields(t *testing.T) {
	firstSaving, releaseFirst := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
	}()
	db := &configStore{before: func() { close(firstSaving); <-releaseFirst }}
	rec := control.RecorderFunc(func(context.Context, string, string, string, string) error { return nil })
	engine := control.New(control.Options{Store: db, Recorder: rec, Channels: 8})
	tokens, err := auth.NewTokens(testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := tokens.Issue("operator", auth.RoleOperator, []string{auth.WriteThresholds})
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Server{Tokens: tokens, Control: engine}).Handler()
	path := "/api/config/control"
	first := requestAsync(handler, "PUT", path, token, strings.NewReader(`{"on_above":34}`))
	await(t, firstSaving)
	secondBody := &startedBody{Reader: strings.NewReader(`{"off_below":22,"fan_channel":2,"enabled":false}`), started: make(chan struct{})}
	second := requestAsync(handler, "PUT", path, token, secondBody)
	await(t, secondBody.started)
	close(releaseFirst)
	for _, pending := range []<-chan *httptest.ResponseRecorder{first, second} {
		if response := await(t, pending); response.Code != 200 {
			t.Fatalf("update = %d: %s", response.Code, response.Body)
		}
	}
	want := control.Config{OnAbove: 34, OffBelow: 22, FanChannel: 2, Enabled: false}
	var saved control.Config
	if err := json.Unmarshal([]byte(db.current), &saved); err != nil || saved != want || engine.Snapshot().Config != want {
		t.Fatalf("saved=%+v memory=%+v want=%+v error=%v", saved, engine.Snapshot().Config, want, err)
	}
}
