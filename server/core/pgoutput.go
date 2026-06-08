package core

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Message types from pgoutput protocol
const (
	msgBegin    = 'B'
	msgCommit   = 'C'
	msgRelation = 'R'
	msgInsert   = 'I'
	msgUpdate   = 'U'
	msgDelete   = 'D'
	msgTruncate = 'T'
)

// RelationCache maps relation_id → table info (populated from 'R' messages)
type RelationCache map[uint32]*RelationMsg

type RelationMsg struct {
	ID        uint32
	Namespace string
	Name      string
	Columns   []Column
}

type Column struct {
	Name string
	Type uint32 // OID
}

type InsertMsg struct {
	RelationID uint32
	Row        map[string]any
}

type UpdateMsg struct {
	RelationID uint32
	OldRow     map[string]any // nil if REPLICA IDENTITY DEFAULT
	NewRow     map[string]any
}

type DeleteMsg struct {
	RelationID uint32
	OldRow     map[string]any
}

// ParseMessages parses a raw pgoutput byte slice into a sequence of typed messages.
// relations cache is updated in place as 'R' messages are encountered.
func ParseMessages(data []byte, relations RelationCache) ([]any, error) {
	var messages []any
	r := &reader{buf: data}

	for r.remaining() > 0 {
		msgType, err := r.readByte()
		if err != nil {
			return nil, err
		}

		switch msgType {
		case msgBegin:
			r.skip(8 + 8 + 4) // lsn + timestamp + xid

		case msgCommit:
			r.skip(1 + 8 + 8 + 8) // flags + commit_lsn + end_lsn + timestamp

		case msgRelation:
			rel, err := parseRelation(r)
			if err != nil {
				return nil, fmt.Errorf("parse relation: %w", err)
			}
			relations[rel.ID] = rel

		case msgInsert:
			msg, err := parseInsert(r, relations)
			if err != nil {
				return nil, fmt.Errorf("parse insert: %w", err)
			}
			messages = append(messages, msg)

		case msgUpdate:
			msg, err := parseUpdate(r, relations)
			if err != nil {
				return nil, fmt.Errorf("parse update: %w", err)
			}
			messages = append(messages, msg)

		case msgDelete:
			msg, err := parseDelete(r, relations)
			if err != nil {
				return nil, fmt.Errorf("parse delete: %w", err)
			}
			messages = append(messages, msg)

		case msgTruncate:
			// skip truncate for now
			count, _ := r.readUint32()
			r.skip(1) // option bits
			r.skip(int(count) * 4)
		}
	}

	return messages, nil
}

func parseRelation(r *reader) (*RelationMsg, error) {
	id, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	namespace, err := r.readString()
	if err != nil {
		return nil, err
	}
	name, err := r.readString()
	if err != nil {
		return nil, err
	}
	r.skip(1) // replica identity

	colCount, err := r.readUint16()
	if err != nil {
		return nil, err
	}

	cols := make([]Column, colCount)
	for i := range cols {
		r.skip(1) // flags
		colName, err := r.readString()
		if err != nil {
			return nil, err
		}
		typeOID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		r.skip(4) // type modifier
		cols[i] = Column{Name: colName, Type: typeOID}
	}

	return &RelationMsg{ID: id, Namespace: namespace, Name: name, Columns: cols}, nil
}

func parseInsert(r *reader, relations RelationCache) (*InsertMsg, error) {
	relID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	tag, err := r.readByte() // always 'N'
	if err != nil {
		return nil, err
	}
	_ = tag

	rel := relations[relID]
	row, err := parseTuple(r, rel)
	if err != nil {
		return nil, err
	}
	return &InsertMsg{RelationID: relID, Row: row}, nil
}

func parseUpdate(r *reader, relations RelationCache) (*UpdateMsg, error) {
	relID, err := r.readUint32()
	if err != nil {
		return nil, err
	}

	rel := relations[relID]
	msg := &UpdateMsg{RelationID: relID}

	tag, err := r.readByte()
	if err != nil {
		return nil, err
	}

	// 'O' = old tuple, 'K' = key tuple, 'N' = new tuple
	if tag == 'O' || tag == 'K' {
		msg.OldRow, err = parseTuple(r, rel)
		if err != nil {
			return nil, err
		}
		tag, err = r.readByte() // should be 'N'
		if err != nil {
			return nil, err
		}
	}
	_ = tag // 'N'

	msg.NewRow, err = parseTuple(r, rel)
	return msg, err
}

func parseDelete(r *reader, relations RelationCache) (*DeleteMsg, error) {
	relID, err := r.readUint32()
	if err != nil {
		return nil, err
	}

	rel := relations[relID]
	tag, err := r.readByte() // 'O' or 'K'
	if err != nil {
		return nil, err
	}
	_ = tag

	row, err := parseTuple(r, rel)
	if err != nil {
		return nil, err
	}
	return &DeleteMsg{RelationID: relID, OldRow: row}, nil
}

func parseTuple(r *reader, rel *RelationMsg) (map[string]any, error) {
	colCount, err := r.readUint16()
	if err != nil {
		return nil, err
	}

	row := make(map[string]any, colCount)
	for i := 0; i < int(colCount); i++ {
		kind, err := r.readByte()
		if err != nil {
			return nil, err
		}

		var colName string
		if rel != nil && i < len(rel.Columns) {
			colName = rel.Columns[i].Name
		} else {
			colName = fmt.Sprintf("col_%d", i)
		}

		switch kind {
		case 'n': // null
			row[colName] = nil
		case 'u': // unchanged (toast)
			row[colName] = nil
		case 't': // text
			length, err := r.readUint32()
			if err != nil {
				return nil, err
			}
			val, err := r.readBytes(int(length))
			if err != nil {
				return nil, err
			}
			row[colName] = string(val)
		}
	}
	return row, nil
}

// --- low-level reader ---

type reader struct {
	buf []byte
	pos int
}

func (r *reader) remaining() int { return len(r.buf) - r.pos }

func (r *reader) readByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, fmt.Errorf("unexpected EOF at pos %d", r.pos)
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) readBytes(n int) ([]byte, error) {
	if r.pos+n > len(r.buf) {
		return nil, fmt.Errorf("unexpected EOF: need %d bytes at pos %d", n, r.pos)
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *reader) readUint16() (uint16, error) {
	b, err := r.readBytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (r *reader) readUint32() (uint32, error) {
	b, err := r.readBytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}


func (r *reader) readString() (string, error) {
	start := r.pos
	for r.pos < len(r.buf) {
		if r.buf[r.pos] == 0 {
			s := string(r.buf[start:r.pos])
			r.pos++ // skip null terminator
			return s, nil
		}
		r.pos++
	}
	return "", fmt.Errorf("unterminated string at pos %d", start)
}

func (r *reader) skip(n int) {
	r.pos += int(math.Min(float64(n), float64(len(r.buf)-r.pos)))
}
