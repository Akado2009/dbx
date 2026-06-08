package core

import (
	"encoding/binary"
	"testing"
)

// helpers to build pgoutput binary messages

func buildString(s string) []byte {
	return append([]byte(s), 0) // null-terminated
}

func buildUint16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func buildUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}


func buildTextDatum(s string) []byte {
	b := []byte{'t'}
	b = append(b, buildUint32(uint32(len(s)))...)
	b = append(b, []byte(s)...)
	return b
}

func buildNullDatum() []byte {
	return []byte{'n'}
}

// buildRelationMsg builds a Relation ('R') message
func buildRelationMsg(relID uint32, namespace, name string, cols []Column) []byte {
	b := []byte{msgRelation}
	b = append(b, buildUint32(relID)...)
	b = append(b, buildString(namespace)...)
	b = append(b, buildString(name)...)
	b = append(b, 'd') // replica identity DEFAULT
	b = append(b, buildUint16(uint16(len(cols)))...)
	for _, col := range cols {
		b = append(b, 0) // not part of PK flag
		b = append(b, buildString(col.Name)...)
		b = append(b, buildUint32(col.Type)...)
		b = append(b, buildUint32(0)...) // type modifier
	}
	return b
}

// buildInsertMsg builds an Insert ('I') message
func buildInsertMsg(relID uint32, values []string) []byte {
	b := []byte{msgInsert}
	b = append(b, buildUint32(relID)...)
	b = append(b, 'N') // new tuple
	b = append(b, buildUint16(uint16(len(values)))...)
	for _, v := range values {
		b = append(b, buildTextDatum(v)...)
	}
	return b
}

// buildUpdateMsg builds an Update ('U') message (no old tuple)
func buildUpdateMsg(relID uint32, newValues []string) []byte {
	b := []byte{msgUpdate}
	b = append(b, buildUint32(relID)...)
	b = append(b, 'N') // new tuple directly
	b = append(b, buildUint16(uint16(len(newValues)))...)
	for _, v := range newValues {
		b = append(b, buildTextDatum(v)...)
	}
	return b
}

// buildDeleteMsg builds a Delete ('D') message
func buildDeleteMsg(relID uint32, oldValues []string) []byte {
	b := []byte{msgDelete}
	b = append(b, buildUint32(relID)...)
	b = append(b, 'O') // old tuple
	b = append(b, buildUint16(uint16(len(oldValues)))...)
	for _, v := range oldValues {
		b = append(b, buildTextDatum(v)...)
	}
	return b
}

// --- Tests ---

func TestParseRelation(t *testing.T) {
	cols := []Column{
		{Name: "id", Type: 23},
		{Name: "email", Type: 25},
	}
	data := buildRelationMsg(1, "public", "users", cols)

	relations := make(RelationCache)
	msgs, err := ParseMessages(data, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Relation messages are cached but not returned
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation in cache, got %d", len(relations))
	}

	rel := relations[1]
	if rel.Name != "users" {
		t.Errorf("expected table name 'users', got %q", rel.Name)
	}
	if rel.Namespace != "public" {
		t.Errorf("expected namespace 'public', got %q", rel.Namespace)
	}
	if len(rel.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(rel.Columns))
	}
	if rel.Columns[0].Name != "id" {
		t.Errorf("expected col[0] = 'id', got %q", rel.Columns[0].Name)
	}
	if rel.Columns[1].Name != "email" {
		t.Errorf("expected col[1] = 'email', got %q", rel.Columns[1].Name)
	}
}

func TestParseInsert(t *testing.T) {
	cols := []Column{{Name: "id", Type: 23}, {Name: "email", Type: 25}}
	relations := RelationCache{
		1: {ID: 1, Namespace: "public", Name: "users", Columns: cols},
	}

	data := buildInsertMsg(1, []string{"42", "foo@bar.com"})
	msgs, err := ParseMessages(data, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	insert, ok := msgs[0].(*InsertMsg)
	if !ok {
		t.Fatalf("expected *InsertMsg, got %T", msgs[0])
	}
	if insert.RelationID != 1 {
		t.Errorf("expected relation_id=1, got %d", insert.RelationID)
	}
	if insert.Row["id"] != "42" {
		t.Errorf("expected id='42', got %v", insert.Row["id"])
	}
	if insert.Row["email"] != "foo@bar.com" {
		t.Errorf("expected email='foo@bar.com', got %v", insert.Row["email"])
	}
}

func TestParseUpdate(t *testing.T) {
	cols := []Column{{Name: "id", Type: 23}, {Name: "email", Type: 25}}
	relations := RelationCache{
		2: {ID: 2, Namespace: "public", Name: "users", Columns: cols},
	}

	data := buildUpdateMsg(2, []string{"42", "new@bar.com"})
	msgs, err := ParseMessages(data, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	update, ok := msgs[0].(*UpdateMsg)
	if !ok {
		t.Fatalf("expected *UpdateMsg, got %T", msgs[0])
	}
	if update.NewRow["email"] != "new@bar.com" {
		t.Errorf("expected email='new@bar.com', got %v", update.NewRow["email"])
	}
}

func TestParseDelete(t *testing.T) {
	cols := []Column{{Name: "id", Type: 23}, {Name: "email", Type: 25}}
	relations := RelationCache{
		3: {ID: 3, Namespace: "public", Name: "orders", Columns: cols},
	}

	data := buildDeleteMsg(3, []string{"99", "del@bar.com"})
	msgs, err := ParseMessages(data, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	del, ok := msgs[0].(*DeleteMsg)
	if !ok {
		t.Fatalf("expected *DeleteMsg, got %T", msgs[0])
	}
	if del.OldRow["id"] != "99" {
		t.Errorf("expected id='99', got %v", del.OldRow["id"])
	}
}

func TestParseNullDatum(t *testing.T) {
	cols := []Column{{Name: "id", Type: 23}, {Name: "bio", Type: 25}}
	relations := RelationCache{
		4: {ID: 4, Namespace: "public", Name: "profiles", Columns: cols},
	}

	// build insert with null bio
	b := []byte{msgInsert}
	b = append(b, buildUint32(4)...)
	b = append(b, 'N')
	b = append(b, buildUint16(2)...)
	b = append(b, buildTextDatum("1")...)
	b = append(b, buildNullDatum()...)

	msgs, err := ParseMessages(b, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	insert := msgs[0].(*InsertMsg)
	if insert.Row["bio"] != nil {
		t.Errorf("expected bio=nil, got %v", insert.Row["bio"])
	}
}

func TestParseMultipleMessages(t *testing.T) {
	cols := []Column{{Name: "id", Type: 23}, {Name: "name", Type: 25}}

	// Relation + 2 Inserts in one buffer
	data := buildRelationMsg(5, "public", "products", cols)
	data = append(data, buildInsertMsg(5, []string{"1", "apple"})...)
	data = append(data, buildInsertMsg(5, []string{"2", "banana"})...)

	relations := make(RelationCache)
	msgs, err := ParseMessages(data, relations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestDetectConflicts(t *testing.T) {
	mainChanges := []*Change{
		{
			Table:      "users",
			Operation:  "UPDATE",
			PrimaryKey: "42",
			NewRow:     map[string]any{"id": "42", "email": "main@foo.com"},
		},
	}
	branchChanges := []*Change{
		{
			Table:      "users",
			Operation:  "UPDATE",
			PrimaryKey: "42",
			NewRow:     map[string]any{"id": "42", "email": "branch@foo.com"},
		},
	}

	conflicts := DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	c := conflicts[0]
	if c.Column != "email" {
		t.Errorf("expected conflict on 'email', got %q", c.Column)
	}
	if c.MainValue != "main@foo.com" {
		t.Errorf("expected main_value='main@foo.com', got %v", c.MainValue)
	}
	if c.YourValue != "branch@foo.com" {
		t.Errorf("expected your_value='branch@foo.com', got %v", c.YourValue)
	}
}

func TestDetectNoConflicts(t *testing.T) {
	mainChanges := []*Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "1", NewRow: map[string]any{"id": "1", "name": "alice"}},
	}
	branchChanges := []*Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "2", NewRow: map[string]any{"id": "2", "name": "bob"}},
	}

	conflicts := DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflictsDifferentTables(t *testing.T) {
	mainChanges := []*Change{
		{Table: "users", Operation: "UPDATE", PrimaryKey: "1", NewRow: map[string]any{"id": "1", "email": "a@b.com"}},
	}
	branchChanges := []*Change{
		{Table: "orders", Operation: "UPDATE", PrimaryKey: "1", NewRow: map[string]any{"id": "1", "email": "a@b.com"}},
	}

	conflicts := DetectConflicts(mainChanges, branchChanges)
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts (different tables), got %d", len(conflicts))
	}
}
