package app

import (
	"reflect"
	"strings"
	"testing"
)

// `xwrt set` has to read a value the way the setting means it, not the way it
// looks. Guessing from the value is a trap with one particularly bad case:
// dns_mode's three values are dnsmasq, redirect and off — and "off" looks
// exactly like a boolean. The daemon then answered with a sentence about Go
// types, for a command our own DNS failure hint tells people to run.
func TestSetReadsValuesTheWayTheSettingMeansThem(t *testing.T) {
	cases := []struct {
		key, val string
		want     any
	}{
		// The case that was broken. A string setting whose value happens to
		// be a word this command used to treat as false.
		{"dns_mode", "off", "off"},
		{"dns_mode", "dnsmasq", "dnsmasq"},
		{"log_level", "none", "none"},
		// A mode is a string too, even the one named after a boolean-ish word.
		{"mode", "redirect", "redirect"},
		// Booleans still read as booleans, in every spelling people use.
		{"auto_connect", "true", true},
		{"auto_connect", "yes", true},
		{"auto_connect", "on", true},
		{"auto_connect", "1", true},
		{"allow_lan", "false", false},
		{"allow_lan", "off", false},
		{"allow_lan", "0", false},
		// Numbers are numbers.
		{"socks_port", "1080", 1080},
		{"route_table", "180", 180},
		// A name that happens to be all digits is still a name, not a
		// number — the same bug wearing different clothes. lan_device stands
		// in for the family here; `active`, where it first showed up, is
		// refused outright now because the daemon owns it.
		{"lan_device", "12345678", "12345678"},
		// A mark is written in hex and must survive as written.
		{"fwmark", "0x1e0", "0x1e0"},
		// Lists split on commas, and an empty value empties the list rather
		// than setting it to one empty string.
		{"bypass_ip", "10.0.0.0/8, 192.168.1.0/24",
			[]string{"10.0.0.0/8", "192.168.1.0/24"}},
		{"bypass_mac", "", []string{}},
	}

	for _, c := range cases {
		got, err := coerce(c.key, c.val)
		if err != nil {
			t.Errorf("%s=%s: %v", c.key, c.val, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s=%s gave %#v, want %#v", c.key, c.val, got, c.want)
		}
	}
}

// A value the setting cannot hold is worth refusing here, where the message
// can name the setting, rather than in the daemon, where it names a Go type.
func TestSetRefusesValuesThatCannotWork(t *testing.T) {
	for _, c := range []struct{ key, val string }{
		{"auto_connect", "sometimes"},
		{"socks_port", "eightyeighty"},
	} {
		if _, err := coerce(c.key, c.val); err == nil {
			t.Errorf("%s=%s was accepted", c.key, c.val)
		}
	}
}

// A typo used to be sent to the daemon, ignored there, and reported as
// success — so `xwrt set socsk_port=1080` looked like a setting that had been
// changed, and the next connect used the old port with no explanation.
func TestSetRefusesASettingThatDoesNotExist(t *testing.T) {
	_, err := coerce("socsk_port", "1080")
	if err == nil {
		t.Fatal("an unknown setting was accepted")
	}
}

// The field list is read from the struct rather than written out here, so a
// setting added later works without anyone remembering this file. That only
// holds while the reading works at all.
func TestEverySettingIsReachable(t *testing.T) {
	fields := settingsFields()
	for _, want := range []string{
		"mode", "dns_mode", "socks_port", "auto_connect", "bypass_mac",
		"proxy_router", "xray_bin", "fwmark",
	} {
		if _, ok := fields[want]; !ok {
			t.Errorf("%s is a setting but `xwrt set` cannot see it", want)
		}
	}
}

// The daemon keeps these three whatever it is sent, so accepting them here
// would report a change that did not happen — and the reader would go looking
// for the reason everywhere except at the command they typed.
func TestSetRefusesTheFieldsTheDaemonOwns(t *testing.T) {
	for _, key := range []string{"enabled", "active", "active_kind"} {
		_, err := coerce(key, "true")
		if err == nil {
			t.Errorf("%s was accepted, but setting it does nothing", key)
			continue
		}
		if !strings.Contains(err.Error(), "xwrt connect") {
			t.Errorf("%s: the refusal does not say what to use instead: %v", key, err)
		}
	}
}
