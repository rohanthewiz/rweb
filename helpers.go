package rweb

// credit fasthttp

import (
	"math/rand"
	"unsafe"
)

// b2s converts byte slice to a string without memory allocation.
// See https://groups.google.com/forum/#!msg/Golang-Nuts/ENgbUzYvCuU/90yGx7GUAgAJ .
func b2s(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// s2b converts string to a byte slice without memory allocation.
func s2b(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// Embed this type into a struct, which mustn't be copied,
// so `go vet` gives a warning if this struct is copied.
//
// See https://github.com/golang/go/issues/8005#issuecomment-190753527 for details.
// and also: https://stackoverflow.com/questions/52494458/nocopy-minimal-example
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

func genRandString(n int, groupByFours bool) string {
	var letterRunes = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

	if groupByFours {
		n += 1     // bc we add a dash at the beginning
		n += n / 4 // for every 4, add a dash
	}

	b := make([]rune, n)
	for i := range b {
		if groupByFours && i%5 == 0 {
			b[i] = '-'
			continue
		}
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}

	if groupByFours {
		b = b[1:] // remove the first dash

		if b[len(b)-1] == '-' { // remove the last dash
			b = b[:len(b)-1]
		}
	}

	return string(b)
}
