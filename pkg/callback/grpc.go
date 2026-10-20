package callback

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func (jsonCodec) Name() string                       { return "json" }

type callbackEvent struct {
	EventType string `json:"event_type"`
	RunID     string `json:"run_id"`
	Timestamp string `json:"timestamp"`
	Payload   string `json:"payload"`
}

type ack struct{}

const sendEventMethod = "/markov.MarkovCallback/SendEvent"

type GRPCCallback struct {
	conn *grpc.ClientConn
}

func NewGRPCCallback(addr string, tlsInsecure bool, tlsCertPath string) (*GRPCCallback, error) {
	opts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})),
	}

	if tlsCertPath != "" {
		certPEM, err := os.ReadFile(tlsCertPath)
		if err != nil {
			return nil, fmt.Errorf("reading TLS cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(certPEM)
		creds := credentials.NewTLS(&tls.Config{RootCAs: pool})
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else if tlsInsecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to gRPC callback server: %w", err)
	}
	return &GRPCCallback{conn: conn}, nil
}

func (g *GRPCCallback) sendEvent(eventType, runID string, ts time.Time, event any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	ce := &callbackEvent{
		EventType: eventType,
		RunID:     runID,
		Timestamp: ts.Format(time.RFC3339Nano),
		Payload:   string(payload),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp ack
	if err := g.conn.Invoke(ctx, sendEventMethod, ce, &resp); err != nil {
		log.Printf("callback grpc: send error: %v", err)
		return err
	}
	return nil
}

func (g *GRPCCallback) OnRunStarted(event RunStartedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnRunCompleted(event RunCompletedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnRunFailed(event RunFailedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnRunPaused(event RunPausedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnRunResumed(event RunResumedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnStepStarted(event StepStartedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnStepCompleted(event StepCompletedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnStepFailed(event StepFailedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnStepSkipped(event StepSkippedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnStepProgress(event StepProgressEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnDebug(event DebugEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnJobCreated(event JobCreatedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnGateEvaluated(event GateEvaluatedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnSubRunStarted(event SubRunStartedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnSubRunCompleted(event SubRunCompletedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}
func (g *GRPCCallback) OnSubRunFailed(event SubRunFailedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}

func (g *GRPCCallback) OnWorkflowReloaded(event WorkflowReloadedEvent) error {
	return g.sendEvent(event.EventType, event.RunID, event.Timestamp, event)
}

func (g *GRPCCallback) Close() error {
	return g.conn.Close()
}
