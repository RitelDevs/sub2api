// Package sse decodes event boundaries independently of the JSON carried by SSE.
package sse

import (
	"bytes"
	"errors"
)

var ErrFrameTooLarge = errors.New("SSE frame exceeds size limit")

// Event data and comments are borrowed until the callback returns.
type Event struct {
	Name    string
	Data    []byte
	Comment []byte
}

// Decoder accepts LF, CRLF and CR across arbitrary transport chunks. The limit
// covers the entire frame including ignored fields, with normalized newlines.
type Decoder struct {
	limit   int
	line    []byte
	data    []byte
	name    string
	size    int
	hasData bool
	started bool
	skipLF  bool
	err     error
}

func NewDecoder(limit int) *Decoder {
	return &Decoder{limit: max(1, limit)}
}

func (d *Decoder) Push(p []byte, consume func(Event) error) error {
	if d.err != nil {
		return d.err
	}
	for len(p) > 0 {
		if d.skipLF {
			d.skipLF = false
			if p[0] == '\n' {
				p = p[1:]
				continue
			}
		}
		end := bytes.IndexAny(p, "\r\n")
		if end < 0 {
			if len(p) > d.limit-d.size-len(d.line) {
				d.err = ErrFrameTooLarge
				return d.err
			}
			d.line = append(d.line, p...)
			return nil
		}
		if end > d.limit-d.size-len(d.line) {
			d.err = ErrFrameTooLarge
			return d.err
		}
		line := p[:end]
		if len(d.line) > 0 {
			d.line = append(d.line, line...)
			line = d.line
		}
		d.skipLF = p[end] == '\r'
		p = p[end+1:]
		d.err = d.consumeLine(line, consume)
		d.line = d.line[:0]
		if d.err != nil {
			return d.err
		}
	}
	return nil
}

// Finish accepts a complete trailing data event without an empty line. Call it
// only after clean input completion; transport errors belong to the caller.
func (d *Decoder) Finish(consume func(Event) error) error {
	if d.err != nil {
		return d.err
	}
	if len(d.line) > 0 {
		d.err = d.consumeLine(d.line, consume)
		d.line = d.line[:0]
		if d.err != nil {
			return d.err
		}
	}
	d.err = d.consumeLine(nil, consume)
	return d.err
}

func (d *Decoder) consumeLine(line []byte, consume func(Event) error) error {
	if !d.started {
		line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		d.started = true
	}
	if len(line) == 0 {
		var err error
		if d.hasData {
			err = consume(Event{Name: d.name, Data: d.data})
		}
		d.data = d.data[:0]
		d.name = ""
		d.size = 0
		d.hasData = false
		return err
	}
	if len(line)+1 > d.limit-d.size {
		return ErrFrameTooLarge
	}
	d.size += len(line) + 1
	if line[0] == ':' {
		return consume(Event{Comment: line})
	}
	field, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "event":
		d.name = string(value)
	case "data":
		if d.hasData {
			d.data = append(d.data, '\n')
		}
		d.data = append(d.data, value...)
		d.hasData = true
	}
	return nil
}
