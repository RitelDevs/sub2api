// Package sse splits an event stream independently of the transport reads.
package sse

import (
	"bytes"
	"errors"
)

var ErrFrameTooLarge = errors.New("SSE event exceeds configured size")

// Slices in Event are valid for the duration of the callback. A comment is
// available immediately, independently of the current data event.
type Event struct {
	Name          string
	Data, Comment []byte
}

type Decoder struct {
	limit, size               int
	partial, data             []byte
	event                     string
	hasData, started, afterCR bool
	err                       error
}

func NewDecoder(limit int) *Decoder { return &Decoder{limit: max(1, limit)} }

func reuse(b []byte) []byte {
	if cap(b) > 64<<10 {
		return nil
	}
	return b[:0]
}

func (d *Decoder) Push(p []byte, consume func(Event) error) error {
	for len(p) != 0 && d.err == nil {
		if d.afterCR {
			d.afterCR = false
			if p[0] == '\n' {
				p = p[1:]
				continue
			}
		}
		i := bytes.IndexAny(p, "\r\n")
		end := i
		if i < 0 {
			end = len(p)
		}
		if end > d.limit-d.size-len(d.partial) {
			d.err = ErrFrameTooLarge
			break
		}
		if i < 0 {
			d.partial = append(d.partial, p...)
			break
		}
		line := p[:i]
		if len(d.partial) != 0 {
			d.partial = append(d.partial, line...)
			line = d.partial
		}
		d.afterCR = p[i] == '\r'
		p = p[i+1:]
		d.err = d.line(line, consume)
		d.partial = reuse(d.partial)
	}
	if d.err != nil {
		d.partial, d.data = nil, nil
	}
	return d.err
}

// Finish is only for a clean EOF. Some Chat servers omit the last empty line.
func (d *Decoder) Finish(consume func(Event) error) error {
	if d.err == nil && len(d.partial) != 0 {
		d.err = d.line(d.partial, consume)
	}
	if d.err == nil {
		d.err = d.line(nil, consume)
	}
	d.partial, d.data = nil, nil
	return d.err
}

func (d *Decoder) line(line []byte, consume func(Event) error) error {
	if !d.started {
		d.started = true
		line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
	}
	if len(line) == 0 {
		var err error
		if d.hasData {
			err = consume(Event{Name: d.event, Data: d.data})
		}
		d.event, d.hasData, d.size = "", false, 0
		d.data = reuse(d.data)
		return err
	}
	if len(line) >= d.limit-d.size {
		return ErrFrameTooLarge
	}
	d.size += len(line) + 1
	if line[0] == ':' {
		return consume(Event{Comment: line})
	}
	field, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "data":
		if d.hasData {
			d.data = append(d.data, '\n')
		}
		d.hasData = true
		d.data = append(d.data, value...)
	case "event":
		d.event = string(value)
	}
	return nil
}
