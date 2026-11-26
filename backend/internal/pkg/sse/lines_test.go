package sse

import (
	"bufio"
	"io"
	"reflect"
	"strings"
	"testing"

	"testing/iotest"
)

func TestSplitLinesTransportBoundaries(t *testing.T) {
	const input = "\xef\xbb\xbfevent: m\r\ndata: x\r\r\ndata: y\n\n"
	want := []string{"event: m", "data: x", "", "data: y", ""}
	for _, r := range []io.Reader{strings.NewReader(input), iotest.OneByteReader(strings.NewReader(input)), iotest.DataErrReader(strings.NewReader(input))} {
		s := bufio.NewScanner(r)
		s.Split(SplitLines())
		var lines []string
		for s.Scan() {
			lines = append(lines, s.Text())
		}
		if s.Err() != nil || !reflect.DeepEqual(lines, want) {
			t.Fatalf("lines %q, error %v", lines, s.Err())
		}
	}
	split := SplitLines()
	if n, line, err := split([]byte("data: x\r"), false); n != 8 || string(line) != "data: x" || err != nil {
		t.Fatalf("lone CR did not finish an open transport line: %d, %q, %v", n, line, err)
	}
}
