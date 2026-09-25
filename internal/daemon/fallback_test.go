package daemon

import (
	"testing"

	"xwrt/internal/model"
)

// What the device is left in when a switch fails.
//
// Switching servers is teardown-then-start. The teardown always works; the
// start is the part with a network in it. So a start that fails used to leave
// the device with no tunnel, no capture rules and a connection that was
// carrying traffic a moment earlier thrown away — because someone tried a
// different server.
//
// On this router that is not a failed switch, it is an outage: there is no way
// out except the tunnel, so the operator finds out by losing the page they
// clicked on, and has no way back to the box to press connect again. The
// failure still has to be reported — it is a real failure and the error stays
// on screen — but the thing that was working should still be working.

func TestAFailedSwitchGoesBackToWhatWasWorking(t *testing.T) {
	if !shouldFallBack("p1", "p2") {
		t.Fatal("a failed switch from one server to another left the device " +
			"with nothing, which on a router with no other way out is an outage")
	}
}

func TestAFailedReconnectToTheSameTargetIsNotRetried(t *testing.T) {
	// It just failed. Trying it again immediately is the same attempt with the
	// same network, and the second failure would file a copy of the first
	// error over the top of it.
	if shouldFallBack("p1", "p1") {
		t.Error("the target that just failed was tried again straight away")
	}
}

func TestThereIsNothingToGoBackToFromNothing(t *testing.T) {
	// A first connect that fails has no previous connection behind it. Falling
	// back to "" would connect to whatever the stored selection happens to be,
	// which is a device choosing a server by itself.
	if shouldFallBack("", "p2") {
		t.Error("a first connect that failed tried to restore a connection " +
			"that never existed")
	}
}

// Which target was connected has to be read correctly, or the fallback
// restores the wrong one — worse than not falling back at all, because it
// reports success while connecting somewhere nobody asked for.

func TestTheConnectedProfileIsWhatGetsRestored(t *testing.T) {
	e := &Engine{connected: true, profile: &model.Profile{ID: "p7", Name: "tek"}}
	if got := e.activeTargetLocked(); got != "p7" {
		t.Errorf("active target = %q, want p7", got)
	}
}

func TestAGroupIsNamedByItsOwnID(t *testing.T) {
	// A group's members are profiles, and reading a member's id here would
	// silently turn a group connection into a single-server one on the way
	// back.
	e := &Engine{
		connected: true,
		group:     &model.Group{ID: "g1", Name: "avrupa"},
		members:   []model.Profile{{ID: "p1"}, {ID: "p2"}},
	}
	if got := e.activeTargetLocked(); got != "g1" {
		t.Errorf("active target = %q, want g1", got)
	}
}

func TestADisconnectedEngineNamesNoTarget(t *testing.T) {
	// The fields outlive the connection in some paths. "Connected to p1" has
	// to mean connected.
	e := &Engine{connected: false, profile: &model.Profile{ID: "p1"}}
	if got := e.activeTargetLocked(); got != "" {
		t.Errorf("a disconnected engine named %q as its active target", got)
	}
}
