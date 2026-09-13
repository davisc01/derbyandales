package bus

import (
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}, false
	}
}

func TestPublishReachesSubscriber(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Publish(TopicRace, "heat.armed", map[string]int{"heat": 7})

	ev, _ := recv(t, ch)
	if ev.Topic != TopicRace || ev.Kind != "heat.armed" {
		t.Fatalf("got %+v", ev)
	}
	if ev.Seq != 1 {
		t.Errorf("Seq = %d, want 1", ev.Seq)
	}
}

func TestTopicFilter(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(TopicTimer)
	defer cancel()

	// Should be filtered out entirely.
	b.Publish(TopicVote, "tally", nil)
	b.Publish(TopicTimer, "gate.closed", nil)

	ev, _ := recv(t, ch)
	if ev.Topic != TopicTimer {
		t.Fatalf("filter leaked %s", ev.Topic)
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected second event: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeAllTopicsWhenNoneGiven(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	for _, topic := range []Topic{TopicTimer, TopicRace, TopicVote, TopicBracket, TopicDisplay, TopicSystem} {
		b.Publish(topic, "x", nil)
	}
	for i := 0; i < 6; i++ {
		recv(t, ch)
	}
}

// A display that stops reading must never be able to stall the timer goroutine.
// Events for it are dropped instead, and the drop is counted.
func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < bufferSize*3; i++ {
			b.Publish(TopicRace, "flood", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}

	if got := b.Drops(); got == 0 {
		t.Error("expected dropped events to be counted")
	}
}

func TestCancelUnsubscribesAndClosesChannel(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()

	if got := b.Subscribers(); got != 1 {
		t.Fatalf("Subscribers() = %d, want 1", got)
	}
	cancel()
	if got := b.Subscribers(); got != 0 {
		t.Fatalf("after cancel Subscribers() = %d, want 0", got)
	}
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after cancel")
	}
	// Cancelling twice must not panic on a double close.
	cancel()
	// Publishing to nobody must not panic either.
	b.Publish(TopicRace, "after-cancel", nil)
}

func TestSeqIncreasesMonotonically(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	for i := 0; i < 5; i++ {
		b.Publish(TopicSystem, "tick", i)
	}
	var last uint64
	for i := 0; i < 5; i++ {
		ev, _ := recv(t, ch)
		if ev.Seq <= last {
			t.Fatalf("Seq went backwards: %d after %d", ev.Seq, last)
		}
		last = ev.Seq
	}
}
