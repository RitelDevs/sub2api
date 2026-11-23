package sse

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecoderFragmentsAndFields(t *testing.T) {
	wire := "\xef\xbb\xbfevent: message\r\nid: one\r\ndata:{\r\ndata:  \"text\":\"你好\"}\r\n\r\n: ping\rdata: tail\r\r"
	for split := 0; split <= len(wire); split++ {
		d := NewDecoder(1024)
		var got []string
		consume := func(e Event) error { got = append(got, e.Name+"|"+string(e.Data)+"|"+string(e.Comment)); return nil }
		require.NoError(t, d.Push([]byte(wire[:split]), consume))
		require.NoError(t, d.Push([]byte(wire[split:]), consume))
		require.NoError(t, d.Finish(consume))
		require.Equal(t, []string{"message|{\n \"text\":\"你好\"}|", "||: ping", "|tail|"}, got, "split=%d", split)
	}
}

func TestDecoderBoundsEntireEvent(t *testing.T) {
	for _, wire := range []string{strings.Repeat("x", 33), "id: 123456789\nid: 123456789\ndata: x\n\n", "data: 123456789\ndata: 123456789\ndata: x\n\n"} {
		d := NewDecoder(32)
		consume := func(Event) error { return nil }
		var err error
		for _, b := range []byte(wire) {
			err = d.Push([]byte{b}, consume)
			if err != nil {
				break
			}
		}
		require.ErrorIs(t, err, ErrFrameTooLarge)
		require.ErrorIs(t, d.Finish(consume), ErrFrameTooLarge)
	}
	d := NewDecoder(8)
	var count int
	consume := func(Event) error { count++; return nil }
	require.NoError(t, d.Push([]byte("data: x\n\ndata: y\n\n"), consume))
	require.Equal(t, 2, count)
}

func TestDecoderTrailingEventAndCallbackError(t *testing.T) {
	d := NewDecoder(64)
	var got []string
	consume := func(e Event) error { got = append(got, string(e.Data)); return nil }
	require.NoError(t, d.Push([]byte("event: ignored\n\ndata: last"), consume))
	require.Empty(t, got)
	require.NoError(t, d.Finish(consume))
	require.Equal(t, []string{"last"}, got)
	failure := errors.New("consumer failed")
	d = NewDecoder(64)
	require.ErrorIs(t, d.Push([]byte("data: a\n\ndata: b\n\n"), func(Event) error { return failure }), failure)
	require.ErrorIs(t, d.Push([]byte("data: c\n\n"), consume), failure)
}
