package coverage

import (
	"encoding/binary"
	"strings"
	"testing"
)

// realHeaderPrefix is the first 40 bytes of a meta-file emitted by
// coverage.WriteMeta from a binary built with `go build -cover` (Go 1.26,
// one package entry). The Go runtime named the files it wrote to GOCOVERDIR
// covmeta.8580f17e010d81d47181091b308fa88f and
// covcounters.8580f17e010d81d47181091b308fa88f.<pid>.<nanotime>, so that hash
// is what a covmeta filename produced from this header has to contain.
var realHeaderPrefix = []byte{
	0x00, 0x63, 0x76, 0x6d, // Magic
	0x01, 0x00, 0x00, 0x00, // Version
	0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // TotalLength
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Entries
	0x85, 0x80, 0xf1, 0x7e, 0x01, 0x0d, 0x81, 0xd4, // MetaFileHash
	0x71, 0x81, 0x09, 0x1b, 0x30, 0x8f, 0xa8, 0x8f,
}

func TestMetaHashFromHeader_RealHeader(t *testing.T) {
	const want = "8580f17e010d81d47181091b308fa88f"

	if got := metaHashFromHeader(realHeaderPrefix); got != want {
		t.Errorf("metaHashFromHeader = %q, want %q", got, want)
	}
}

// Entries sits immediately before MetaFileHash and is the field the hash used
// to be read from. It is the package count, so it is near-constant across
// binaries: reading from there throws away half the hash and prefixes every
// filename with the same bytes.
func TestMetaHashFromHeader_SkipsEntriesField(t *testing.T) {
	got := metaHashFromHeader(realHeaderPrefix)

	entries := binary.LittleEndian.Uint64(realHeaderPrefix[16:24])
	if entries == 0 {
		t.Fatal("fixture header has no package entries; it cannot detect the offset slip")
	}
	if strings.HasPrefix(got, "0100000000000000") {
		t.Errorf("metaHashFromHeader = %q; the hash starts inside the Entries field", got)
	}
	if len(got) != 2*metaHashLen {
		t.Errorf("metaHashFromHeader returned %d hex digits, want %d", len(got), 2*metaHashLen)
	}
}

func TestMetaHashFromHeader_ShortBuffer(t *testing.T) {
	for _, size := range []int{0, 24, metaHashOffset + metaHashLen - 1} {
		if got := metaHashFromHeader(make([]byte, size)); got != "unknown" {
			t.Errorf("metaHashFromHeader(%d bytes) = %q, want %q", size, got, "unknown")
		}
	}
}
