package sse

import (
	"bufio"
	"bytes"
)

// SplitLines reads SSE lines, including a lone CR, and strips the optional
// initial BOM. A trailing CR completes its line without waiting for more data.
func SplitLines() bufio.SplitFunc {
	first, skipLF := true, false
	scanned := 0
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if len(data) == 0 {
			return 0, nil, nil
		}
		prefix := 0
		if skipLF {
			skipLF = false
			if data[0] == '\n' {
				prefix = 1
				data = data[1:]
				if len(data) == 0 {
					return prefix, nil, nil
				}
			}
		}
		end := len(data)
		advance := end
		if i := bytes.IndexAny(data[scanned:], "\r\n"); i >= 0 {
			end = scanned + i
			advance = end + 1
			if data[end] == '\r' {
				if advance < len(data) && data[advance] == '\n' {
					advance++
				} else {
					skipLF = advance == len(data)
				}
			}
		} else if !atEOF {
			scanned = len(data)
			return prefix, nil, nil
		}
		scanned = 0
		line := data[:end]
		if first {
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
			first = false
		}
		return prefix + advance, line, nil
	}
}
