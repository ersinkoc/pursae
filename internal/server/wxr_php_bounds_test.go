package server

import (
	"strconv"
	"testing"
)

// A string length near MaxInt in PHP-serialized meta overflowed r.i+n and passed the bounds check,
// so slicing panicked; it must be an ordinary parse error.
func TestPHPUnserializeHugeStringLength(t *testing.T) {
	for _, v := range []string{
		`s:` + strconv.Itoa(int(^uint(0)>>1)) + `:"x";`,
		`s:` + strconv.Itoa(int(^uint(0)>>1)-1) + `:"ab";`,
		`a:1:{s:` + strconv.Itoa(int(^uint(0)>>1)) + `:"k";i:1;}`,
		`s:5:"abc";`,
		`s:-1:"";`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("phpUnserialize(%q) panicked: %v", v, r)
				}
			}()
			if _, err := phpUnserialize(v); err == nil {
				t.Errorf("phpUnserialize(%q) accepted a length past the end", v)
			}
		}()
	}
	// Exact lengths still parse.
	if got, err := phpUnserialize(`s:3:"abc";`); err != nil || got != "abc" {
		t.Errorf(`s:3:"abc"; = %v, %v`, got, err)
	}
}
