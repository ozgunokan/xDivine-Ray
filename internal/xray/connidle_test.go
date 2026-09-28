package xray

import (
	"testing"

	"xwrt/internal/model"
)

// How long a connection may sit idle before the core closes it.
//
// This is a correctness setting, not a tuning knob. A phone holds one
// connection to its push service open for as long as it is switched on and
// sends nothing down it for hours. Close it and the phone does not find out
// until its next heartbeat, then backs off further after the failure — what the
// owner sees is notifications arriving late or not at all.

func TestTheDefaultOutlastsASleepingPhone(t *testing.T) {
	got := connIdleFor(&model.Settings{})
	// An hour is not enough: a phone in doze goes far longer than that between
	// heartbeats. The number itself can move, but not below this.
	if got < 3*3600 {
		t.Errorf("idle timeout %d seconds; a push connection is idle for hours "+
			"by design and closing it is what makes notifications go missing", got)
	}
}

func TestDefaultsCarryIt(t *testing.T) {
	if model.Defaults().ConnIdle < 3*3600 {
		t.Errorf("the shipped default is %d seconds", model.Defaults().ConnIdle)
	}
}

func TestAnOperatorsValueIsUsed(t *testing.T) {
	if got := connIdleFor(&model.Settings{ConnIdle: 600}); got != 600 {
		t.Errorf("connIdleFor = %d; the setting exists so somebody can change it", got)
	}
}

// Zero means "not set", not "close immediately" and not the core's own 300.
// A configuration written before this setting existed has no value for it.
func TestAnUnsetValueFallsBackToTheDefault(t *testing.T) {
	if got := connIdleFor(&model.Settings{ConnIdle: 0}); got != model.Defaults().ConnIdle {
		t.Errorf("connIdleFor(0) = %d, want the default %d",
			got, model.Defaults().ConnIdle)
	}
}

// And it reaches the generated configuration, which is the only thing the core
// ever reads.
func TestItReachesTheCoreConfiguration(t *testing.T) {
	s := model.Defaults()
	s.ConnIdle = 7200
	cfg, err := Build(Options{
		Profile: &model.Profile{
			ID: "p1", Proto: model.ProtoVLESS, Address: "1.2.3.4", Port: 443,
			UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
		},
		Settings: &s,
		Caps:     AllFeatures(),
	})
	if err != nil {
		t.Fatal(err)
	}
	level := cfg.Policy.Levels["0"]
	if level == nil || level.ConnIdle != 7200 {
		t.Fatalf("the core config carries %+v, want connIdle 7200", level)
	}
}

// Normalize fills it in, so a configuration loaded from an older file does not
// reach the core as zero — which the core reads as its own 300 seconds.
func TestNormalizeFillsItIn(t *testing.T) {
	var s model.Settings
	s.Normalize()
	if s.ConnIdle != model.Defaults().ConnIdle {
		t.Errorf("after Normalize ConnIdle = %d, want %d",
			s.ConnIdle, model.Defaults().ConnIdle)
	}
}
