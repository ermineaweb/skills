package jsonschema

import (
	"errors"
	"testing"
)

const testSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["slot_id", "debut"],
  "properties": {
    "slot_id": {"type": "string", "minLength": 1},
    "debut":   {"type": "string", "format": "date-time"},
    "duree":   {"type": ["integer", "null"], "minimum": 5, "maximum": 480},
    "email":   {"type": ["string", "null"], "format": "email"},
    "mode":    {"type": "string", "enum": ["a", "b"]}
  }
}`

func TestValidate(t *testing.T) {
	s := MustCompile([]byte(testSchema))

	cases := []struct {
		name        string
		doc         string
		wantOK      bool
		wantMissing []string
	}{
		{"valide", `{"slot_id":"x","debut":"2026-10-01T14:00:00+02:00","duree":30,"email":null}`, true, nil},
		{"champ requis absent", `{"debut":"2026-10-01T14:00:00+02:00"}`, false, []string{"slot_id"}},
		{"champ requis null", `{"slot_id":null,"debut":"2026-10-01T14:00:00+02:00"}`, false, []string{"slot_id"}},
		{"date en langage naturel", `{"slot_id":"x","debut":"jeudi après-midi"}`, false, nil},
		{"date sans fuseau", `{"slot_id":"x","debut":"2026-10-01T14:00:00"}`, false, nil},
		{"entier attendu", `{"slot_id":"x","debut":"2026-10-01T14:00:00Z","duree":30.5}`, false, nil},
		{"minimum", `{"slot_id":"x","debut":"2026-10-01T14:00:00Z","duree":1}`, false, nil},
		{"propriété inconnue", `{"slot_id":"x","debut":"2026-10-01T14:00:00Z","hack":true}`, false, nil},
		{"email invalide", `{"slot_id":"x","debut":"2026-10-01T14:00:00Z","email":"pas-un-mail"}`, false, nil},
		{"enum", `{"slot_id":"x","debut":"2026-10-01T14:00:00Z","mode":"c"}`, false, nil},
		{"json invalide", `{`, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Validate([]byte(tc.doc))
			if tc.wantOK {
				if err != nil {
					t.Fatalf("attendu valide, obtenu: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("attendu ValidationError, obtenu: %v", err)
			}
			if tc.wantMissing != nil && (len(ve.Missing) != len(tc.wantMissing) || ve.Missing[0] != tc.wantMissing[0]) {
				t.Fatalf("missing = %v, attendu %v", ve.Missing, tc.wantMissing)
			}
		})
	}
}
