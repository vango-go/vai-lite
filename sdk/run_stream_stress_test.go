package vai

import (
	"context"
	"testing"
	"time"
)

func TestRunStream_CloseCancel_Stress(t *testing.T) {
	const iterations = 100

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		provider := newScriptedProvider("test", finalTextTurnEventsForRunStream("ok"))
		svc := newMessagesServiceForRunStreamTest(provider)

		stream, err := svc.RunStream(ctx, &MessageRequest{
			Model: "test/fake-model",
			Messages: []Message{
				{Role: "user", Content: Text("Say ok")},
			},
			MaxTokens: 256,
		})
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: RunStream() error = %v", i, err)
		}

		eventsDone := make(chan struct{})
		go func() {
			defer close(eventsDone)
			for range stream.Events() {
			}
		}()

		closeDone := make(chan struct{})
		go func() {
			defer close(closeDone)
			_ = stream.Cancel()
			_ = stream.Close()
		}()

		select {
		case <-eventsDone:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatalf("iteration %d: events channel did not close", i)
		}

		select {
		case <-closeDone:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatalf("iteration %d: close/cancel goroutine blocked", i)
		}

		// Close is idempotent and should return quickly even after completion.
		if err := stream.Close(); err != nil {
			cancel()
			t.Fatalf("iteration %d: second Close() error = %v", i, err)
		}

		cancel()
	}
}
