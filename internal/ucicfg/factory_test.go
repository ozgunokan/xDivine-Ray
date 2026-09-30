package ucicfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A factory reset has to leave the device exactly as a fresh install does, or
// it is a third state nobody has tested: not the configuration someone built,
// and not the one the package ships.
//
// So this loads the file the package really installs, through the same loader
// the daemon uses, and compares it field by field with what a reset writes.
func TestAResetDeviceIsAFreshlyInstalledOne(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join("..", "..", "package", "xwrt", "files", "etc", "config", "xwrt"))
	if err != nil {
		t.Fatalf("cannot read the shipped configuration: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xwrt"), shipped, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XWRT_CONFDIR", dir)

	fresh, err := NewStore(New()).Load()
	if err != nil {
		t.Fatalf("the shipped file does not load: %v", err)
	}
	reset := Factory()

	a, b := reflect.ValueOf(fresh.Settings), reflect.ValueOf(reset.Settings)
	for i := 0; i < a.NumField(); i++ {
		name := a.Type().Field(i).Name
		if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			t.Errorf("%s: a fresh install has %#v, a reset writes %#v",
				name, a.Field(i).Interface(), b.Field(i).Interface())
		}
	}
	if len(fresh.Profiles)+len(fresh.Groups)+len(fresh.Rules)+len(fresh.Subscriptions) != 0 {
		t.Errorf("the shipped file carries servers, groups, rules or subscriptions")
	}
}

// The lists are empty, not absent. A JSON null where the interface expects a
// list is how an empty page turns into a page that does not load.
func TestAResetHasEmptyListsNotMissingOnes(t *testing.T) {
	b, _ := json.Marshal(Factory())
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"profiles", "groups", "rules", "subscriptions"} {
		v, ok := m[k].([]any)
		if !ok || len(v) != 0 {
			t.Errorf("%s is %#v, want an empty list", k, m[k])
		}
	}
}

// And it survives being written and read back, which is what actually happens
// to it on a device.
func TestAResetRoundTrips(t *testing.T) {
	t.Setenv("XWRT_CONFDIR", t.TempDir())
	s := NewStore(New())
	if err := s.Save(Factory()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Settings, Factory().Settings) {
		t.Errorf("a reset does not read back as itself:\n got  %+v\n want %+v", got.Settings, Factory().Settings)
	}
}
