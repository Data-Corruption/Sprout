package prompt

import (
	"bytes"
	"testing"
)

func TestIntR(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{"simple", "42\n", 42, false},
		{"negative", "-3\n", -3, false},
		{"retry-after-invalid", "x\n7\n", 7, false}, // first token invalid, second ok
		{"eof", "", 0, true},
		{"eof-after-invalid", "x\n", 0, true},
	}

	for _, tc := range tests {
		tc := tc // capture
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := intR(bytes.NewBufferString(tc.in), "p?")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUintR(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    uint
		wantErr bool
	}{
		{"simple", "9\n", 9, false},
		{"zero", "0\n", 0, false},
		{"retry-after-negative", "-1\n8\n", 8, false},
		{"eof", "", 0, true},
		{"eof-after-invalid", "-1\n", 0, true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := uintR(bytes.NewBufferString(tc.in), "p?")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStringR(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "hello\n", "hello", false},
		{"trim-space", "  hi there   \n", "hi there", false},
		{"empty", "\n", "", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := stringR(bytes.NewBufferString(tc.in), "p?")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSecretR(t *testing.T) {
	var output bytes.Buffer
	got, err := secretR(bytes.NewBufferString("  keep spaces  \n"), &output, "Password")
	if err != nil {
		t.Fatalf("secretR: %v", err)
	}
	if got != "  keep spaces  " {
		t.Fatalf("got %q, want spaces preserved", got)
	}
	if output.String() != "Password: " {
		t.Fatalf("prompt = %q, want %q", output.String(), "Password: ")
	}
}

func TestYesNoR(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    bool
		wantErr bool
	}{
		{"yes-lower", "y\n", true, false},
		{"yes-upper", "YES\n", true, false},
		{"no-mixed", "No\n", false, false},
		{"retry-after-junk", "maybe\nn\n", false, false},
		{"eof", "", false, true},
		{"eof-after-junk", "maybe\n", false, true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := yesNoR(bytes.NewBufferString(tc.in), "continue?")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
