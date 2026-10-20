package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type httpEvent struct {
	data []byte
	done chan struct{}
}

type HTTPCallback struct {
	url       string
	client    *http.Client
	headers   map[string]string
	ch        chan httpEvent
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func NewHTTPCallback(url string, headers map[string]string, bufferSize int) *HTTPCallback {
	if bufferSize <= 0 {
		bufferSize = 1000
	}
	h := &HTTPCallback{
		url:     url,
		client:  &http.Client{Timeout: 30 * time.Second},
		headers: headers,
		ch:      make(chan httpEvent, bufferSize),
	}
	h.wg.Add(1)
	go h.sendLoop()
	return h
}

func (h *HTTPCallback) sendLoop() {
	defer h.wg.Done()
	for ev := range h.ch {
		h.sendWithRetry(ev.data)
		if ev.done != nil {
			close(ev.done)
		}
	}
}

func (h *HTTPCallback) sendWithRetry(data []byte) {
	backoffs := []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond}

	for attempt := 0; attempt < 3; attempt++ {
		if backoffs[attempt] > 0 {
			time.Sleep(backoffs[attempt])
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(data))
		if err != nil {
			cancel()
			log.Printf("callback http: request error: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range h.headers {
			req.Header.Set(k, v)
		}

		resp, err := h.client.Do(req)
		cancel()
		if err != nil {
			log.Printf("callback http: attempt %d failed: %v", attempt+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		if resp.StatusCode >= 500 {
			log.Printf("callback http: attempt %d got %d", attempt+1, resp.StatusCode)
			continue
		}
		log.Printf("callback http: got %d, not retrying", resp.StatusCode)
		return
	}
	log.Printf("callback http: event dropped after 3 attempts")
}

func (h *HTTPCallback) send(event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	h.ch <- httpEvent{data: data}
	return nil
}

func (h *HTTPCallback) sendAndFlush(event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	done := make(chan struct{})
	h.ch <- httpEvent{data: data, done: done}
	<-done
	return nil
}

func (h *HTTPCallback) OnRunStarted(event RunStartedEvent) error             { return h.send(event) }
func (h *HTTPCallback) OnRunCompleted(event RunCompletedEvent) error         { return h.sendAndFlush(event) }
func (h *HTTPCallback) OnRunFailed(event RunFailedEvent) error               { return h.sendAndFlush(event) }
func (h *HTTPCallback) OnRunPaused(event RunPausedEvent) error               { return h.sendAndFlush(event) }
func (h *HTTPCallback) OnRunResumed(event RunResumedEvent) error             { return h.send(event) }
func (h *HTTPCallback) OnStepStarted(event StepStartedEvent) error           { return h.send(event) }
func (h *HTTPCallback) OnStepCompleted(event StepCompletedEvent) error       { return h.send(event) }
func (h *HTTPCallback) OnStepFailed(event StepFailedEvent) error             { return h.send(event) }
func (h *HTTPCallback) OnStepSkipped(event StepSkippedEvent) error           { return h.send(event) }
func (h *HTTPCallback) OnStepProgress(event StepProgressEvent) error         { return h.send(event) }
func (h *HTTPCallback) OnDebug(event DebugEvent) error                       { return h.send(event) }
func (h *HTTPCallback) OnJobCreated(event JobCreatedEvent) error             { return h.send(event) }
func (h *HTTPCallback) OnGateEvaluated(event GateEvaluatedEvent) error       { return h.send(event) }
func (h *HTTPCallback) OnSubRunStarted(event SubRunStartedEvent) error       { return h.send(event) }
func (h *HTTPCallback) OnSubRunCompleted(event SubRunCompletedEvent) error   { return h.send(event) }
func (h *HTTPCallback) OnSubRunFailed(event SubRunFailedEvent) error         { return h.send(event) }
func (h *HTTPCallback) OnWorkflowReloaded(event WorkflowReloadedEvent) error { return h.send(event) }

func (h *HTTPCallback) Close() error {
	h.closeOnce.Do(func() {
		close(h.ch)
	})

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("callback http: close timed out, some events may be lost")
	}
	return nil
}
