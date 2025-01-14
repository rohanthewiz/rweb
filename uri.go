package rweb

/*import (
	"bytes"
	"path/filepath"

	"github.com/rohanthewiz/rweb/consts"
)

func addLeadingSlash(dst, src []byte) []byte {
	// zero length 、"C:/" and "a" case
	isDisk := len(src) > 2 && src[1] == ':'
	if len(src) == 0 || (!isDisk && src[0] != '/') {
		dst = append(dst, '/')
	}

	return dst
}

func normalizePath(dst, src []byte) []byte {
	dst = dst[:0]
	dst = addLeadingSlash(dst, src)
	dst = decodeArgAppendNoPlus(dst, src)

	// remove duplicate slashes
	b := dst
	bSize := len(b)
	for {
		n := bytes.Index(b, consts.StrSlashSlash)
		if n < 0 {
			break
		}
		b = b[n:]
		copy(b, b[1:])
		b = b[:len(b)-1]
		bSize--
	}
	dst = dst[:bSize]

	// remove /./ parts
	b = dst
	for {
		n := bytes.Index(b, consts.StrSlashDotSlash)
		if n < 0 {
			break
		}
		nn := n + len(consts.StrSlashDotSlash) - 1
		copy(b[n:], b[nn:])
		b = b[:len(b)-nn+n]
	}

	// remove /foo/../ parts
	for {
		n := bytes.Index(b, consts.StrSlashDotDotSlash)
		if n < 0 {
			break
		}
		nn := bytes.LastIndexByte(b[:n], '/')
		if nn < 0 {
			nn = 0
		}
		n += len(consts.StrSlashDotDotSlash) - 1
		copy(b[nn:], b[n:])
		b = b[:len(b)-n+nn]
	}

	// remove trailing /foo/..
	n := bytes.LastIndex(b, consts.StrSlashDotDot)
	if n >= 0 && n+len(consts.StrSlashDotDot) == len(b) {
		nn := bytes.LastIndexByte(b[:n], '/')
		if nn < 0 {
			return append(dst[:0], consts.StrSlash...)
		}
		b = b[:nn+1]
	}

	if filepath.Separator == '\\' {
		// remove \.\ parts
		for {
			n := bytes.Index(b, consts.StrBackSlashDotBackSlash)
			if n < 0 {
				break
			}
			nn := n + len(consts.StrSlashDotSlash) - 1
			copy(b[n:], b[nn:])
			b = b[:len(b)-nn+n]
		}

		// remove /foo/..\ parts
		for {
			n := bytes.Index(b, consts.StrSlashDotDotBackSlash)
			if n < 0 {
				break
			}
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				nn = 0
			}
			nn++
			n += len(consts.StrSlashDotDotBackSlash)
			copy(b[nn:], b[n:])
			b = b[:len(b)-n+nn]
		}

		// remove /foo\..\ parts
		for {
			n := bytes.Index(b, consts.StrBackSlashDotDotBackSlash)
			if n < 0 {
				break
			}
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				nn = 0
			}
			n += len(consts.StrBackSlashDotDotBackSlash) - 1
			copy(b[nn:], b[n:])
			b = b[:len(b)-n+nn]
		}

		// remove trailing \foo\..
		n := bytes.LastIndex(b, consts.StrBackSlashDotDot)
		if n >= 0 && n+len(consts.StrSlashDotDot) == len(b) {
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				return append(dst[:0], consts.StrSlash...)
			}
			b = b[:nn+1]
		}
	}

	return b
}
*/
