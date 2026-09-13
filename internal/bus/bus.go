// Package bus is the in-process publish/subscribe hub that carries state
// changes from the timer and race controller out to every connected browser.
//
// This replaces DerbyNet's three independent pollers (a 500 ms timer heartbeat,
// a 500 ms content poll, and a 5 s kiosk poll). Displays hold one SSE
// connection each and are updated the instant a result lands, rather than up to
// five seconds later.
package bus

import (
	"sync"
	"sync/atomic"
)

// Topic names a class of event. Subscribers filter on it.
type Topic string

const (
	// TopicTimer carries timer connection, gate and result events.
	TopicTimer Topic = "timer"
	// TopicRace carries heat arming, results and standings changes.
	TopicRace Topic = "race"
	// TopicVote carries ballot tallies.
	TopicVote Topic = "vote"
	// TopicBracket carries matchup and round progression.
	TopicBracket Topic = "bracket"
	// TopicDisplay carries scene assignments to connected screens.
	TopicDisplay Topic = "display"
	// TopicSystem carries preflight, backup and shutdown notices.
	TopicSystem Topic = "system"
)

// Event is one published message. Data must be JSON-marshalable; it is encoded
// once per SSE connection by the web layer.
type Event struct {
	Topic Topic  `json:"topic"`
	Kind  string `json:"kind"`
	Data  any    `json:"data,omitempty"`
	Seq   uint64 `json:"seq"`
}

// subscriber is one connected client.
type subscriber struct {
	id     uint64
	topics map[Topic]bool
	ch     chan Event
}

// Bus fans events out to subscribers. A slow or disconnected subscriber is
// never allowed to block a publisher: its buffer fills, further events for it
// are dropped, and a drop counter is incremented. Losing a frame on a display
// is recoverable (the next event re-renders it); stalling the timer goroutine
// is not.
type Bus struct {
	mu     sync.RWMutex
	subs   map[uint64]*subscriber
	nextID uint64
	seq    uint64
	drops  atomic.Uint64
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: make(map[uint64]*subscriber)}
}

// bufferSize is per-subscriber. Deep enough to absorb a burst of lane results
// plus a standings recompute while a browser is mid-render.
const bufferSize = 64

// Subscribe registers for the given topics, or for everything when none are
// given. The returned cancel func must be called to release the subscription.
func (b *Bus) Subscribe(topics ...Topic) (<-chan Event, func()) {
	s := &subscriber{
		ch:     make(chan Event, bufferSize),
		topics: make(map[Topic]bool, len(topics)),
	}
	for _, t := range topics {
		s.topics[t] = true
	}

	b.mu.Lock()
	b.nextID++
	s.id = b.nextID
	b.subs[s.id] = s
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s.id)
			b.mu.Unlock()
			close(s.ch)
		})
	}
	return s.ch, cancel
}

// Publish delivers an event to every interested subscriber. It never blocks.
func (b *Bus) Publish(topic Topic, kind string, data any) {
	ev := Event{
		Topic: topic,
		Kind:  kind,
		Data:  data,
		Seq:   atomic.AddUint64(&b.seq, 1),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if len(s.topics) > 0 && !s.topics[topic] {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.drops.Add(1)
		}
	}
}

// Subscribers reports how many clients are currently attached. The preflight
// panel uses this to show how many displays are online.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Drops reports how many events were discarded because a subscriber could not
// keep up. A non-zero value here during a race points at a wedged browser.
func (b *Bus) Drops() uint64 { return b.drops.Load() }
