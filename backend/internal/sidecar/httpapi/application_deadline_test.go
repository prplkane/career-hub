package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deadlineCall(h *handler, mode string, w http.ResponseWriter, ctx context.Context, call func() (*ApplicationOutcome, *ApplicationError)) {
	switch mode {
	case "locked":
		h.applicationCall(w, ctx, http.StatusOK, call)
	case "selected":
		h.applicationCallByOutcome(w, ctx, func(*ApplicationOutcome) int { return http.StatusOK }, call)
	default:
		h.applicationCallUnlocked(w, ctx, http.StatusOK, call)
	}
}

func TestApplicationResponseDeadline(t *testing.T) {
	for _, mode := range []string{"locked", "selected", "unlocked"} {
		t.Run(mode, func(t *testing.T) {
			for _, budget := range []time.Duration{20, 60, 120, 150} {
				ctx, cancel := context.WithTimeout(context.Background(), budget*time.Second)
				deadline, _ := ctx.Deadline()
				w := &materialDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
				deadlineCall(&handler{}, mode, w, ctx, func() (*ApplicationOutcome, *ApplicationError) {
					if !w.deadline.Equal(deadline.Add(5 * time.Second)) {
						t.Error("response deadline not installed before owner")
					}
					return &ApplicationOutcome{Result: json.RawMessage(`{"saved":true}`)}, nil
				})
				cancel()
				if w.Code != http.StatusOK {
					t.Fatalf("status = %d", w.Code)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			w := &materialDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), failure: errors.New("private deadline detail")}
			deadlineCall(&handler{}, mode, w, ctx, func() (*ApplicationOutcome, *ApplicationError) {
				t.Fatal("failed deadline entered owner")
				return nil, nil
			})
			if w.Code != 500 || !strings.Contains(w.Body.String(), "LOCAL_REQUEST_DEADLINE_FAILED") || strings.Contains(w.Body.String(), "private deadline detail") {
				t.Fatalf("unsafe deadline failure: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestApplicationResponseOutlivesListenerDeadline(t *testing.T) {
	for _, mode := range []string{"locked", "selected", "unlocked"} {
		t.Run(mode, func(t *testing.T) {
			h := &handler{}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), time.Second)
				defer cancel()
				deadlineCall(h, mode, w, ctx, func() (*ApplicationOutcome, *ApplicationError) {
					time.Sleep(120 * time.Millisecond)
					return &ApplicationOutcome{Result: json.RawMessage(`{"saved":true}`), Committed: true}, nil
				})
			}))
			server.Config.WriteTimeout = 25 * time.Millisecond
			server.Start()
			defer server.Close()
			client := server.Client()
			client.Timeout = 2 * time.Second
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatalf("completed operation lost its response: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"saved":true`) {
				t.Fatalf("response: status=%d body=%s err=%v", response.StatusCode, body, err)
			}
		})
	}
}

type notifyingDeadlineWriter struct {
	*materialDeadlineWriter
	ready chan struct{}
}

func (w *notifyingDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	err := w.materialDeadlineWriter.SetWriteDeadline(deadline)
	close(w.ready)
	return err
}

func TestApplicationResponseDeadlinePrecedesGateWait(t *testing.T) {
	for _, mode := range []string{"locked", "selected"} {
		t.Run(mode, func(t *testing.T) {
			h := &handler{}
			h.mu.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			w := &notifyingDeadlineWriter{materialDeadlineWriter: &materialDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}, ready: make(chan struct{})}
			done := make(chan struct{})
			called := false
			go func() {
				defer close(done)
				deadlineCall(h, mode, w, ctx, func() (*ApplicationOutcome, *ApplicationError) {
					called = true
					return &ApplicationOutcome{Result: json.RawMessage(`{}`)}, nil
				})
			}()
			ready := false
			select {
			case <-w.ready:
				ready = true
			case <-time.After(time.Second):
			}
			cancel()
			h.mu.Unlock()
			<-done
			if !ready || called || w.Code != http.StatusRequestTimeout {
				t.Fatalf("gate/deadline ordering: ready=%v owner=%v status=%d", ready, called, w.Code)
			}
		})
	}
}

func TestApplicationResponseDeadlineKeepsCancellationFacts(t *testing.T) {
	for _, mode := range []string{"locked", "selected", "unlocked"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// A recorder intentionally does not implement deadlines (ErrNotSupported).
			w := httptest.NewRecorder()
			deadlineCall(&handler{}, mode, w, ctx, func() (*ApplicationOutcome, *ApplicationError) {
				cancel()
				return &ApplicationOutcome{Committed: true, Partial: true, CommitUnknown: true}, nil
			})
			var result struct {
				State map[string]any `json:"operation_state"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 408 || result.State["committed"] != true || result.State["partial"] != true || result.State["known"] != false {
				t.Fatalf("cancellation erased outcome: %s (%v)", w.Body.String(), err)
			}
		})
	}
}

func TestApplicationResponseDeadlineRequiresBoundedContext(t *testing.T) {
	w := httptest.NewRecorder()
	deadlineCall(&handler{}, "unlocked", w, context.Background(), func() (*ApplicationOutcome, *ApplicationError) {
		t.Fatal("unbounded operation entered owner")
		return nil, nil
	})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "LOCAL_REQUEST_DEADLINE_FAILED") {
		t.Fatalf("unbounded context: %d %s", w.Code, w.Body.String())
	}
}
