package app

import "testing"

func TestNextBroadcastMessage(t *testing.T) {
	blocks := []broadcastBlock{
		{id: 1, kind: "delay", delay: 3600},
		{id: 2, kind: "message", key: "first"},
		{id: 3, kind: "message", key: "second"},
		{id: 4, kind: "delay", delay: 120},
	}
	for _, tc := range []struct {
		name     string
		after    int64
		wantID   int64
		wantWait int
	}{
		{"initial delay", 0, 2, 3600},
		{"adjacent messages", 2, 3, 0},
		{"delays across cycle", 3, 2, 3720},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, _, wait, ok := nextBroadcastMessage(blocks, tc.after)
			if !ok || id != tc.wantID || wait != tc.wantWait {
				t.Fatalf("next = (%d, %d, %t), want (%d, %d, true)", id, wait, ok, tc.wantID, tc.wantWait)
			}
		})
	}
	id, _, wait, ok := nextBroadcastMessage([]broadcastBlock{{id: 1, kind: "message", key: "only"}}, 1)
	if !ok || id != 1 || wait != 3600 {
		t.Fatalf("cycle without delay = (%d, %d, %t)", id, wait, ok)
	}
	id, _, wait, ok = nextBroadcastMessage([]broadcastBlock{
		{id: 1, kind: "delay", delay: 1},
		{id: 2, kind: "message", key: "first"},
	}, 0)
	if !ok || id != 2 || wait != 1 {
		t.Fatalf("one-second delay = (%d, %d, %t)", id, wait, ok)
	}
}

func TestNextMailingMessageDoesNotRepeat(t *testing.T) {
	blocks := []broadcastBlock{
		{id: 1, kind: "delay", delay: 2},
		{id: 2, kind: "message", key: "first"},
		{id: 3, kind: "delay", delay: 5},
		{id: 4, kind: "message", key: "last"},
		{id: 5, kind: "delay", delay: 100},
	}
	for _, tc := range []struct {
		after, wantID int64
		wantDelay     int
		wantFound     bool
	}{
		{0, 2, 2, true},
		{2, 4, 5, true},
		{4, 0, 0, false},
	} {
		id, _, delay, found := nextMailingMessage(blocks, tc.after)
		if id != tc.wantID || delay != tc.wantDelay || found != tc.wantFound {
			t.Fatalf("after %d: got (%d,%d,%v), want (%d,%d,%v)", tc.after, id, delay, found, tc.wantID, tc.wantDelay, tc.wantFound)
		}
	}
}
