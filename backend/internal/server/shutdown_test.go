package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestG2ShutdownWaitsForAcceptedWrite(t *testing.T) {
	f := newAPI(t)
	entered, release := make(chan struct{}), make(chan struct{})
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; f.r.ServeHTTP(w, r) })}
	defer srv.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	type result struct {
		Status int
		Err    error
	}
	response := make(chan result, 1)
	go func() {
		req, e := http.NewRequest("POST", "http://"+listener.Addr().String()+"/api/v2/submissions", strings.NewReader(`{"content":{}}`))
		if e != nil {
			response <- result{Err: e}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("X-Idempotency-Key", "shutdown-write")
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			response <- result{Err: e}
			return
		}
		defer r.Body.Close()
		response <- result{Status: r.StatusCode}
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown := make(chan error, 1)
	go func() { shutdown <- srv.Shutdown(ctx) }()
	select {
	case e := <-shutdown:
		close(release)
		t.Fatal("shutdown returned before accepted request completed", e)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	r := <-response
	if r.Err != nil || r.Status != 201 {
		t.Fatal("accepted write lost", r)
	}
	if e := <-shutdown; e != nil {
		t.Fatal(e)
	}
	if e := <-serveDone; !errors.Is(e, http.ErrServerClosed) {
		t.Fatal(e)
	}
	var count int
	if e := f.db.Get(&count, `SELECT COUNT(*) FROM inspection_submissions`); e != nil || count != 1 {
		t.Fatal("in-flight write did not commit", e)
	}
}
