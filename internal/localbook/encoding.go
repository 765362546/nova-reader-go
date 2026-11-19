package localbook

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText detects the charset and returns UTF-8 text plus the charset name.
func DecodeText(b []byte) (string, string) {
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		return string(b[3:]), "UTF-8"
	}
	if utf8.Valid(b) {
		return string(b), "UTF-8"
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err == nil {
		return string(decoded), "GBK"
	}
	// fallback: strip invalid utf8
	var sb strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			b = b[1:]
			continue
		}
		sb.WriteRune(r)
		b = b[size:]
	}
	return sb.String(), "UTF-8"
}
