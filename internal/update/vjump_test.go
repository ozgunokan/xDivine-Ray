package update

import "testing"

// The jump from 0.x to 1.0.0. A device running 0.25.2 must be offered it, and
// a string comparison would say "0.25.2" > "1.0.0" is false only by luck —
// this checks the numbers.
func TestTheFirstStableReleaseIsOfferedToEveryZeroPointVersion(t *testing.T) {
	for _, old := range []string{"0.25.2", "0.25.0", "0.9.0", "0.10.3", "0.1.0"} {
		if !Newer("1.0.0", old) {
			t.Errorf("a device on %s would not be offered 1.0.0", old)
		}
		if Newer(old, "1.0.0") {
			t.Errorf("1.0.0 would be offered %s as an update", old)
		}
	}
	if Newer("1.0.0", "1.0.0") {
		t.Error("1.0.0 offered as an update to itself")
	}
	if !Newer("1.0.1", "1.0.0") || !Newer("1.1.0", "1.0.9") {
		t.Error("releases after 1.0.0 are not recognised as newer")
	}
}
