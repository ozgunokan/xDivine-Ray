package netenv

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// Asking the processor whether it does AES, by making Go choose.
//
// The answer decides which TLS fingerprint is the fast one here: with no
// hardware AES, every browser fingerprint negotiates AES-GCM and the device
// does AES in software for every byte, while the undisguised fingerprint lets
// Go put ChaCha20 first and roughly doubles the throughput. With hardware AES
// it makes no difference at all. So the recommendation is only as good as this
// probe, and a probe that always answered the same thing would look perfectly
// healthy while recommending nothing, or recommending it to everybody.
//
// Which is the hard part to test: the machine running the test has whatever CPU
// it has. The lever is Go's own: GODEBUG=cpu.aes=off takes the feature away at
// startup, which is exactly the condition a MIPS router is permanently in. The
// test re-runs itself under it and the answer has to change.

func TestTheProbeReachesAnAnswer(t *testing.T) {
	got := AESAccel()
	if got != AccelYes && got != AccelNo {
		t.Fatalf("the probe answered %q; empty means the handshake did not "+
			"happen, and then nothing can be recommended to anybody", got)
	}
}

func TestTheAnswerDoesNotChangeUnderfoot(t *testing.T) {
	// It is a fact about the processor, cached once. Two callers disagreeing
	// would mean the status page and the log could say different things.
	first := AESAccel()
	for i := 0; i < 20; i++ {
		if got := AESAccel(); got != first {
			t.Fatalf("call %d answered %q where the first answered %q", i, got, first)
		}
	}
}

// The probe must agree with the thing it refuses to read. /proc/cpuinfo is not
// used because it spells the answer differently on every architecture and a
// word this code did not know to look for is indistinguishable from a feature
// the CPU does not have — but where the file does name it, the two had better
// say the same thing, or one of them is wrong.
func TestItAgreesWithWhatTheKernelSays(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("there is no /proc/cpuinfo to compare against")
	}
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		t.Skip("no /proc/cpuinfo on this machine")
	}
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "flags") || strings.HasPrefix(low, "features") {
			line = low
			break
		}
	}
	if line == "" {
		t.Skip("this kernel does not list CPU features, which is the whole " +
			"reason the probe does not read this file")
	}
	kernelSaysAES := false
	for _, f := range strings.Fields(line) {
		if f == "aes" {
			kernelSaysAES = true
			break
		}
	}
	want := AccelNo
	if kernelSaysAES {
		want = AccelYes
	}
	if got := AESAccel(); got != want {
		t.Fatalf("the kernel lists aes=%v and the probe says %q", kernelSaysAES, got)
	}
}

// --- and the part that proves it is measuring anything ------------------

// theChildEnv marks the re-run so the child runs the one check and exits.
const theChildEnv = "XWRT_AES_PROBE_CHILD"

func TestTakingAESAwayChangesTheAnswer(t *testing.T) {
	if os.Getenv(theChildEnv) != "" {
		// Running as the child, with the feature switched off.
		if got := AESAccel(); got != AccelNo {
			t.Fatalf("with cpu.aes=off the probe still says %q, so it is not "+
				"reading Go's preference order at all and the recommendation "+
				"this feeds is guesswork", got)
		}
		return
	}

	if AESAccel() != AccelYes {
		t.Skip("this processor has no hardware AES to take away; the probe " +
			"already says no, which is the state this test produces")
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestTakingAESAwayChangesTheAnswer",
		"-test.v")
	cmd.Env = append(os.Environ(), theChildEnv+"=1", "GODEBUG=cpu.aes=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// GODEBUG's cpu options are per-architecture. A platform with no such
		// knob cannot be tested this way, and saying so is better than failing
		// on it — but a real failure has to still fail, so the two are told
		// apart by what the child printed.
		if strings.Contains(string(out), "unknown cpu feature") ||
			strings.Contains(string(out), "GODEBUG: no cpu feature") {
			t.Skipf("this architecture has no GODEBUG switch for AES:\n%s", out)
		}
		t.Fatalf("the child failed:\n%s", out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("the child did not pass:\n%s", out)
	}
}
