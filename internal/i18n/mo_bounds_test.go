package i18n

import (
	"encoding/binary"
	"strings"
	"testing"
)

// TestParseMOHugeCount: the entry count of a .mo header does not size an allocation the file
// cannot back; a short file claiming two billion entries fails fast.
func TestParseMOHugeCountBounded(t *testing.T) {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint32(b, 0x950412de)
	binary.LittleEndian.PutUint32(b[8:], 0x7fffffff)
	binary.LittleEndian.PutUint32(b[12:], 28)
	binary.LittleEndian.PutUint32(b[16:], 28)
	if _, err := parseMO(b); err == nil {
		t.Fatal("a truncated table must be an error")
	}
}

func TestParseMOBadTableOffset(t *testing.T) {
	for _, table := range []int{12, 16} {
		b := make([]byte, 44)
		binary.LittleEndian.PutUint32(b, 0x950412de)
		binary.LittleEndian.PutUint32(b[8:], 1)
		binary.LittleEndian.PutUint32(b[12:], 28)
		binary.LittleEndian.PutUint32(b[16:], 36)
		binary.LittleEndian.PutUint32(b[table:], 0xffffffff)
		if _, err := parseMO(b); err == nil {
			t.Errorf("offset at %d was accepted", table)
		}
	}
}

// TestCompilePluralTooLong: a Plural-Forms expression of thousands of parentheses is refused
// instead of recursing until the stack limit kills the process.
func TestCompilePluralTooLong(t *testing.T) {
	if _, err := compilePlural(strings.Repeat("(", 1<<20) + "n" + strings.Repeat(")", 1<<20)); err == nil {
		t.Fatal("expected an error")
	}
	r, err := compilePlural("(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2)")
	if err != nil || r.index(1) != 0 || r.index(3) != 1 || r.index(5) != 2 || r.index(22) != 1 {
		t.Fatalf("a real rule: %v", err)
	}
}
