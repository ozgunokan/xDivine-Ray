package model

import "testing"

// The pin goes straight into the core's config, so both spellings a person is
// likely to have must arrive as the plain hex the core expects — and anything
// else must be rejected rather than passed through, because a malformed pin
// makes the core refuse every certificate and the failure then looks like a
// network fault.
func TestPinnedCertHex(t *testing.T) {
	const want = "fe7044b7e2af9455c338d9fcdf9033fadbc292721b33ea8340cd0de74941b105"

	cases := []struct {
		name, in, want string
	}{
		{"plain hex", want, want},
		{"upper case", "FE7044B7E2AF9455C338D9FCDF9033FADBC292721B33EA8340CD0DE74941B105", want},
		{"openssl colon form",
			"FE:70:44:B7:E2:AF:94:55:C3:38:D9:FC:DF:90:33:FA:DB:C2:92:72:1B:33:EA:83:40:CD:0D:E7:49:41:B1:05",
			want},
		{"surrounding space", "  " + want + "  ", want},
		{"empty", "", ""},
		{"too short", "fe7044b7", ""},
		{"not hex", "zz7044b7e2af9455c338d9fcdf9033fadbc292721b33ea8340cd0de74941b1zz", ""},
		{"base64 chain digest from an older version",
			"47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=", ""},
	}
	for _, c := range cases {
		p := Profile{PinnedCert: c.in}
		if got := p.PinnedCertHex(); got != c.want {
			t.Errorf("%s: PinnedCertHex() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPinnedCertValid(t *testing.T) {
	if !(&Profile{}).PinnedCertValid() {
		t.Error("no pin at all is valid")
	}
	if !(&Profile{PinnedCert: "  "}).PinnedCertValid() {
		t.Error("blank is treated as no pin")
	}
	if (&Profile{PinnedCert: "nonsense"}).PinnedCertValid() {
		t.Error("a malformed pin must not be reported valid")
	}
	good := "fe7044b7e2af9455c338d9fcdf9033fadbc292721b33ea8340cd0de74941b105"
	if !(&Profile{PinnedCert: good}).PinnedCertValid() {
		t.Error("a real digest must be valid")
	}
}
