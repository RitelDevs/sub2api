package sse

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestFramingAndMemory(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n", "\r"} {
		d := NewDecoder(1 << 20)
		wire := "\uFEFFevent: reply" + nl + "data: one" + nl + ": ping" + nl + "data: two" + nl + nl + "data: tail"
		var events []string
		consume := func(e Event) error {
			if e.Comment != nil {
				events = append(events, string(e.Comment))
			} else {
				events = append(events, e.Name+"="+string(e.Data))
			}
			return nil
		}
		for _, b := range []byte(wire) {
			if err := d.Push([]byte{b}, consume); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Finish(consume); err != nil {
			t.Fatal(err)
		}
		if strings.Join(events, "|") != ": ping|reply=one\ntwo|=tail" {
			t.Fatal(events)
		}
	}
	d := NewDecoder(1 << 20)
	ignore := func(Event) error { return nil }
	for _, p := range [][]byte{[]byte("data: "), bytes.Repeat([]byte("x"), 200<<10), []byte("\n\n")} {
		if err := d.Push(p, ignore); err != nil {
			t.Fatal(err)
		}
	}
	if cap(d.partial) > 64<<10 || cap(d.data) > 64<<10 {
		t.Fatal("large backing arrays retained after dispatch")
	}
	d = NewDecoder(20)
	if err := d.Push([]byte("ignored: 1234\ndata: 5678\n\n"), ignore); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("aggregate limit: %v", err)
	}
}
