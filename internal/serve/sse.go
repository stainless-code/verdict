package serve

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	defaultHeartbeat = 15 * time.Second
	sseWriteTimeout  = 30 * time.Second
	eventBufferSize  = 1
)

type eventFrame struct {
	change query.Change
	id     uint64
}

type eventBroker struct {
	clients map[uint64]chan eventFrame
	nextID  uint64
	nextSub uint64
	mu      sync.Mutex
}

func newEventBroker() *eventBroker {
	return &eventBroker{clients: map[uint64]chan eventFrame{}}
}

func (b *eventBroker) subscribe() (uint64, <-chan eventFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSub++
	channel := make(chan eventFrame, eventBufferSize)
	b.clients[b.nextSub] = channel
	return b.nextSub, channel
}

func (b *eventBroker) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if channel, ok := b.clients[id]; ok {
		delete(b.clients, id)
		close(channel)
	}
}

func (b *eventBroker) publish(change *query.Change) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded := boundedChange(change)
	b.nextID++
	frame := eventFrame{id: b.nextID, change: bounded}
	for _, channel := range b.clients {
		select {
		case channel <- frame:
		default:
			select {
			case <-channel:
			default:
			}
			dropped := bounded
			dropped.Truncated = true
			channel <- eventFrame{id: frame.id, change: dropped}
		}
	}
}

func cloneChange(change *query.Change) query.Change {
	changed := make([]string, len(change.Changed))
	copy(changed, change.Changed)
	removed := make([]string, len(change.Removed))
	copy(removed, change.Removed)
	return query.Change{
		At: change.At, Root: change.Root, Via: change.Via,
		Changed: changed, Removed: removed, Truncated: change.Truncated,
	}
}

func boundedChange(change *query.Change) query.Change {
	bounded := cloneChange(change)
	if len(bounded.Changed) > query.MaxEventBundles {
		bounded.Changed = bounded.Changed[:query.MaxEventBundles]
		bounded.Truncated = true
	}
	remaining := query.MaxEventBundles - len(bounded.Changed)
	if len(bounded.Removed) > remaining {
		bounded.Removed = bounded.Removed[:remaining]
		bounded.Truncated = true
	}
	return bounded
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.problem(w, http.StatusInternalServerError, "stream-unsupported", "streaming is unavailable")
		return
	}
	subscription, events := s.events.subscribe()
	defer s.events.unsubscribe(subscription)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	s.streamEvents(r, w, flusher, events)
}

func (s *Server) streamEvents(r *http.Request, w http.ResponseWriter, flusher http.Flusher, events <-chan eventFrame) {
	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	for {
		if !s.streamEvent(r, w, flusher, events, heartbeat.C) {
			return
		}
	}
}

func (s *Server) streamEvent(
	r *http.Request,
	w http.ResponseWriter,
	flusher http.Flusher,
	events <-chan eventFrame,
	heartbeat <-chan time.Time,
) bool {
	select {
	case <-r.Context().Done():
		return false
	case <-s.streamsDone:
		return false
	case frame, open := <-events:
		return s.flushEvent(w, flusher, &frame, open)
	case <-heartbeat:
		return s.flushHeartbeat(w, flusher)
	}
}

func (s *Server) flushEvent(w http.ResponseWriter, flusher http.Flusher, frame *eventFrame, open bool) bool {
	if !open || !s.writeEvent(w, frame) {
		return false
	}
	flusher.Flush()
	return true
}

func (s *Server) flushHeartbeat(w http.ResponseWriter, flusher http.Flusher) bool {
	if !s.writeSSE(w, ": ping\n\n") {
		return false
	}
	flusher.Flush()
	return true
}

func (s *Server) writeEvent(w http.ResponseWriter, frame *eventFrame) bool {
	data := jq.NewObj(
		"root", frame.change.Root,
		"via", frame.change.Via,
		"changed", frame.change.Changed,
		"removed", frame.change.Removed,
		"truncated", frame.change.Truncated,
	)
	envelope := core.RenderEnvelope("serve", "", "", core.NowUTC(frame.change.At), "", nil, data, nil)
	return s.writeSSE(w, fmt.Sprintf("event: bundles\nid: %d\nretry: 2000\ndata: %s\n\n",
		frame.id, envelope.MarshalCompact()))
}

func (s *Server) writeSSE(w http.ResponseWriter, text string) bool {
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(sseWriteTimeout)
	if err := controller.SetWriteDeadline(deadline); err != nil {
		return false
	}
	_, err := fmt.Fprint(w, text)
	return err == nil
}

func (s *Server) pumpChanges(ctx context.Context) {
	watcher, ok := s.layer.(changeSource)
	if !ok {
		return
	}
	for change := range watcher.Changes(ctx) {
		s.events.publish(&change)
	}
}
