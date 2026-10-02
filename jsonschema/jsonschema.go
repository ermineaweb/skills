// Package jsonschema implémente un sous-ensemble de JSON Schema suffisant pour
// valider les paramètres de tools, sans dépendance externe.
//
// Mots-clés supportés : type (chaîne ou liste, y compris "null"), properties,
// required, additionalProperties (booléen), enum, minLength, maxLength,
// minimum, maximum, pattern, format ("date-time", "email"), items.
// Les autres mots-clés (description, default, examples…) sont ignorés.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Schema est un schéma compilé.
type Schema struct {
	Types                []string
	Properties           map[string]*Schema
	Required             []string
	AdditionalProperties bool
	Enum                 []any
	MinLength            *int
	MaxLength            *int
	Minimum              *float64
	Maximum              *float64
	Format               string
	Pattern              *regexp.Regexp
	Items                *Schema
}

type rawSchema struct {
	Type                 json.RawMessage            `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Enum                 []any                      `json:"enum"`
	MinLength            *int                       `json:"minLength"`
	MaxLength            *int                       `json:"maxLength"`
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
	Format               string                     `json:"format"`
	Pattern              string                     `json:"pattern"`
	Items                json.RawMessage            `json:"items"`
}

// Compile analyse un schéma JSON.
func Compile(data []byte) (*Schema, error) {
	var raw rawSchema
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("jsonschema: schéma invalide: %w", err)
	}
	s := &Schema{
		Required:             raw.Required,
		AdditionalProperties: true,
		Enum:                 raw.Enum,
		MinLength:            raw.MinLength,
		MaxLength:            raw.MaxLength,
		Minimum:              raw.Minimum,
		Maximum:              raw.Maximum,
		Format:               raw.Format,
	}
	if len(raw.Type) > 0 {
		var one string
		if err := json.Unmarshal(raw.Type, &one); err == nil {
			s.Types = []string{one}
		} else if err := json.Unmarshal(raw.Type, &s.Types); err != nil {
			return nil, fmt.Errorf("jsonschema: 'type' invalide: %w", err)
		}
	}
	if len(raw.AdditionalProperties) > 0 {
		trimmed := bytes.TrimSpace(raw.AdditionalProperties)
		switch string(trimmed) {
		case "false":
			s.AdditionalProperties = false
		case "true":
		default:
			return nil, fmt.Errorf("jsonschema: seul un booléen est supporté pour additionalProperties")
		}
	}
	if raw.Pattern != "" {
		re, err := regexp.Compile(raw.Pattern)
		if err != nil {
			return nil, fmt.Errorf("jsonschema: pattern invalide: %w", err)
		}
		s.Pattern = re
	}
	if len(raw.Properties) > 0 {
		s.Properties = make(map[string]*Schema, len(raw.Properties))
		for name, sub := range raw.Properties {
			compiled, err := Compile(sub)
			if err != nil {
				return nil, fmt.Errorf("propriété %q: %w", name, err)
			}
			s.Properties[name] = compiled
		}
	}
	if len(raw.Items) > 0 {
		items, err := Compile(raw.Items)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		s.Items = items
	}
	return s, nil
}

// MustCompile est comme Compile mais panique en cas d'erreur (schémas embarqués).
func MustCompile(data []byte) *Schema {
	s, err := Compile(data)
	if err != nil {
		panic(err)
	}
	return s
}

// Issue décrit une violation du schéma.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError regroupe les violations. Missing liste les propriétés
// obligatoires absentes (ou nulles), séparément des autres violations.
type ValidationError struct {
	Missing []string
	Issues  []Issue
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues)+1)
	if len(e.Missing) > 0 {
		parts = append(parts, "champs obligatoires manquants: "+strings.Join(e.Missing, ", "))
	}
	for _, is := range e.Issues {
		parts = append(parts, is.Path+": "+is.Message)
	}
	return strings.Join(parts, "; ")
}

// Validate valide un document JSON.
func (s *Schema) Validate(document []byte) error {
	var value any
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return &ValidationError{Issues: []Issue{{Path: "$", Message: "JSON invalide"}}}
	}
	ve := &ValidationError{}
	s.validate("$", value, ve)
	if len(ve.Missing) == 0 && len(ve.Issues) == 0 {
		return nil
	}
	return ve
}

func (s *Schema) validate(path string, value any, ve *ValidationError) {
	if len(s.Types) > 0 && !s.matchesType(value) {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("type attendu %s", strings.Join(s.Types, "|"))})
		return
	}
	if len(s.Enum) > 0 && !inEnum(value, s.Enum) {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("valeur non autorisée (attendu: %v)", s.Enum)})
	}
	switch v := value.(type) {
	case string:
		s.validateString(path, v, ve)
	case json.Number:
		s.validateNumber(path, v, ve)
	case map[string]any:
		s.validateObject(path, v, ve)
	case []any:
		if s.Items != nil {
			for i, item := range v {
				s.Items.validate(fmt.Sprintf("%s[%d]", path, i), item, ve)
			}
		}
	}
}

func (s *Schema) validateString(path, v string, ve *ValidationError) {
	n := len([]rune(v))
	if s.MinLength != nil && n < *s.MinLength {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("longueur minimale %d", *s.MinLength)})
	}
	if s.MaxLength != nil && n > *s.MaxLength {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("longueur maximale %d", *s.MaxLength)})
	}
	if s.Pattern != nil && !s.Pattern.MatchString(v) {
		ve.Issues = append(ve.Issues, Issue{path, "format non respecté"})
	}
	switch s.Format {
	case "date-time":
		// RFC 3339 impose un décalage horaire explicite (Z ou ±hh:mm).
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			ve.Issues = append(ve.Issues, Issue{path, "date-heure ISO 8601 avec fuseau attendue (ex: 2026-10-01T14:00:00+02:00)"})
		}
	case "email":
		if !emailRe.MatchString(v) {
			ve.Issues = append(ve.Issues, Issue{path, "adresse e-mail invalide"})
		}
	}
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func (s *Schema) validateNumber(path string, v json.Number, ve *ValidationError) {
	f, err := v.Float64()
	if err != nil {
		ve.Issues = append(ve.Issues, Issue{path, "nombre invalide"})
		return
	}
	if s.Minimum != nil && f < *s.Minimum {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("minimum %v", *s.Minimum)})
	}
	if s.Maximum != nil && f > *s.Maximum {
		ve.Issues = append(ve.Issues, Issue{path, fmt.Sprintf("maximum %v", *s.Maximum)})
	}
}

func (s *Schema) validateObject(path string, obj map[string]any, ve *ValidationError) {
	for _, name := range s.Required {
		if v, ok := obj[name]; !ok || v == nil || v == "" {
			ve.Missing = append(ve.Missing, joinPath(path, name))
		}
	}
	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sub, known := s.Properties[name]
		if !known {
			if !s.AdditionalProperties {
				ve.Issues = append(ve.Issues, Issue{joinPath(path, name), "propriété non autorisée"})
			}
			continue
		}
		// Une propriété obligatoire nulle est déjà signalée comme manquante.
		if obj[name] == nil && contains(s.Required, name) {
			continue
		}
		sub.validate(joinPath(path, name), obj[name], ve)
	}
}

func (s *Schema) matchesType(value any) bool {
	for _, t := range s.Types {
		switch t {
		case "null":
			if value == nil {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "number":
			if _, ok := value.(json.Number); ok {
				return true
			}
		case "integer":
			if n, ok := value.(json.Number); ok {
				if f, err := n.Float64(); err == nil && f == math.Trunc(f) {
					return true
				}
			}
		}
	}
	return false
}

func inEnum(value any, enum []any) bool {
	for _, e := range enum {
		if fmt.Sprint(e) == fmt.Sprint(value) {
			return true
		}
	}
	return false
}

func joinPath(path, name string) string {
	if path == "$" {
		return name
	}
	return path + "." + name
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
