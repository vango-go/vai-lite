package session

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

type wsWriter interface {
	SetWriteDeadline(t time.Time) error
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}

type outboundWriter struct {
	ws         wsWriter
	ctx        context.Context
	cfg        Config
	priority   <-chan outboundFrame
	normal     <-chan outboundFrame
	isCanceled func(string) bool
}

func (w *outboundWriter) Run() error {
	if w == nil || w.ws == nil {
		return nil
	}

	pingInterval := w.cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = 20 * time.Second
	}
	writeTimeout := w.cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 5 * time.Second
	}

	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	var pendingNormal *outboundFrame

	for {
		if w.ctx != nil {
			select {
			case <-w.ctx.Done():
				w.flushPriorityOnShutdown(writeTimeout)
				_ = w.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(writeTimeout))
				_ = w.ws.Close()
				return nil
			default:
			}
		}

		// Hard priority: if anything is queued, handle it before writing normal frames.
		select {
		case frame, ok := <-w.priority:
			if !ok {
				w.priority = nil
				continue
			}
			if err := w.writeFrame(frame, writeTimeout); err != nil {
				return err
			}
			continue
		default:
		}

		// If we have a pending normal frame, allow a newly-queued priority frame to preempt
		// before we write it.
		if pendingNormal != nil {
			select {
			case frame, ok := <-w.priority:
				if !ok {
					w.priority = nil
					continue
				}
				if err := w.writeFrame(frame, writeTimeout); err != nil {
					return err
				}
				continue
			default:
			}
			if err := w.writeFrame(*pendingNormal, writeTimeout); err != nil {
				return err
			}
			pendingNormal = nil
			continue
		}

		// Exit cleanly if both channels are closed and there's nothing left to write.
		if w.priority == nil && w.normal == nil {
			return nil
		}

		select {
		case <-pingTicker.C:
			deadline := time.Now().Add(writeTimeout)
			if err := w.ws.WriteControl(websocket.PingMessage, []byte("ping"), deadline); err != nil {
				return err
			}
		case frame, ok := <-w.priority:
			if !ok {
				w.priority = nil
				continue
			}
			if err := w.writeFrame(frame, writeTimeout); err != nil {
				return err
			}
		case frame, ok := <-w.normal:
			if !ok {
				w.normal = nil
				continue
			}
			pendingNormal = &frame
		}
	}
}

func (w *outboundWriter) flushPriorityOnShutdown(writeTimeout time.Duration) {
	if w == nil || w.ws == nil || w.priority == nil {
		return
	}

	flushTimeout := 100 * time.Millisecond
	if writeTimeout > 0 && writeTimeout < flushTimeout {
		flushTimeout = writeTimeout
	}
	if flushTimeout <= 0 {
		return
	}

	deadline := time.Now().Add(flushTimeout)
	maxFlushFrames := 8

	for i := 0; i < maxFlushFrames && time.Now().Before(deadline); i++ {
		select {
		case frame, ok := <-w.priority:
			if !ok {
				return
			}
			_ = w.writeFrame(frame, writeTimeout)
		default:
			return
		}
	}
}

func (w *outboundWriter) writeFrame(frame outboundFrame, writeTimeout time.Duration) error {
	if frame.isAssistantAudio && w.isCanceled != nil && w.isCanceled(frame.assistantAudioID) {
		return nil
	}

	deadline := time.Now().Add(writeTimeout)

	if frame.binaryPair != nil {
		if err := w.ws.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if err := w.ws.WriteMessage(websocket.TextMessage, frame.binaryPair.header); err != nil {
			return err
		}
		if err := w.ws.SetWriteDeadline(deadline); err != nil {
			return err
		}
		return w.ws.WriteMessage(websocket.BinaryMessage, frame.binaryPair.data)
	}

	if len(frame.textPayload) > 0 {
		if err := w.ws.SetWriteDeadline(deadline); err != nil {
			return err
		}
		return w.ws.WriteMessage(websocket.TextMessage, frame.textPayload)
	}
	if len(frame.binaryPayload) > 0 {
		if err := w.ws.SetWriteDeadline(deadline); err != nil {
			return err
		}
		return w.ws.WriteMessage(websocket.BinaryMessage, frame.binaryPayload)
	}

	return nil
}
