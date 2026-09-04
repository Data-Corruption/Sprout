//go:build windows

package maintenance

import "testing"

func TestOpenStateReaderAllowsAtomicReplacement(t *testing.T) {
	l := testLayout(t)
	first := readyState()
	if err := WriteState(l, first); err != nil {
		t.Fatalf("write first state: %v", err)
	}

	reader, err := openStateFile(l.State)
	if err != nil {
		t.Fatalf("open state reader: %v", err)
	}
	defer reader.Close()

	second := first
	second.Version = "v1.2.4"
	if err := WriteState(l, second); err != nil {
		t.Fatalf("replace state while reader is open: %v", err)
	}
	got, err := ReadState(l)
	if err != nil {
		t.Fatalf("read replacement state: %v", err)
	}
	if got != second {
		t.Fatalf("state = %#v, want %#v", got, second)
	}
}
