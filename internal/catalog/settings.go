package catalog

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Setting value types a manifest may declare (ADR 0095).
const (
	TypeString  = "string"
	TypeInteger = "integer"
	TypeBoolean = "boolean"
	TypeList    = "list" // a list of strings
)

var settingTypes = []string{TypeString, TypeInteger, TypeBoolean, TypeList}

var settingName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// SettingSchema declares one configurable setting: [settings.<name>] in a
// manifest, or [processes.fields.<name>] for a process-definition field.
type SettingSchema struct {
	Type        string `toml:"type"`
	Description string `toml:"description"`
	Default     any    `toml:"default"`
	Required    bool   `toml:"required"`
	Enum        []any  `toml:"enum"`
}

// ProcessSchema declares the shape of the named process definitions a
// package interprets ([processes] in a manifest). Each definition the config
// writes under [<table>.<name>.processes.<process>] is a table of these
// fields. Stages stay engine-owned: a process is the package's own input.
type ProcessSchema struct {
	Description string                   `toml:"description"`
	Fields      map[string]SettingSchema `toml:"fields"`
}

// validateSettingsSchema checks a manifest's settings and processes schema
// is self-consistent before any config value is judged against it.
func validateSettingsSchema(label string, settings map[string]SettingSchema, processes *ProcessSchema) error {
	if err := validateFields(label+" [settings]", settings); err != nil {
		return err
	}
	if processes == nil {
		return nil
	}
	if len(processes.Fields) == 0 {
		return fmt.Errorf("%s [processes]: declare at least one field under [processes.fields]", label)
	}
	return validateFields(label+" [processes.fields]", processes.Fields)
}

func validateFields(label string, fields map[string]SettingSchema) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := fields[name]
		at := label + "." + name
		if !settingName.MatchString(name) {
			return fmt.Errorf("%s: %q is not a setting name (lowercase letters, digits, '_' or '-')", label, name)
		}
		if !slices.Contains(settingTypes, s.Type) {
			return fmt.Errorf("%s: type %q must be one of %s", at, s.Type, strings.Join(settingTypes, ", "))
		}
		if s.Required && s.Default != nil {
			return fmt.Errorf("%s: a required setting has no default; drop one of them", at)
		}
		if len(s.Enum) > 0 {
			if s.Type != TypeString && s.Type != TypeInteger {
				return fmt.Errorf("%s: enum applies only to string and integer settings", at)
			}
			for _, v := range s.Enum {
				if err := s.checkType(v); err != nil {
					return fmt.Errorf("%s: enum value %v: %w", at, v, err)
				}
			}
		}
		if s.Default != nil {
			if err := s.Check(s.Default); err != nil {
				return fmt.Errorf("%s: default: %w", at, err)
			}
		}
	}
	return nil
}

// Check reports whether v is a valid value for the setting, in TOML's
// decoded form (string, int64, bool, []any of strings).
func (s SettingSchema) Check(v any) error {
	if err := s.checkType(v); err != nil {
		return err
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e any) bool { return e == v }) {
		allowed := make([]string, len(s.Enum))
		for i, e := range s.Enum {
			allowed[i] = fmt.Sprint(e)
		}
		return fmt.Errorf("%s is not one of %s", Describe(v), strings.Join(allowed, ", "))
	}
	return nil
}

func (s SettingSchema) checkType(v any) error {
	ok := false
	switch s.Type {
	case TypeString:
		_, ok = v.(string)
	case TypeInteger:
		_, ok = v.(int64)
	case TypeBoolean:
		_, ok = v.(bool)
	case TypeList:
		list, isList := v.([]any)
		ok = isList
		for _, item := range list {
			if _, isString := item.(string); !isString {
				ok = false
			}
		}
	}
	if !ok {
		return fmt.Errorf("%s is not %s", Describe(v), typeNoun[s.Type])
	}
	return nil
}

var typeNoun = map[string]string{
	TypeString:  "a string",
	TypeInteger: "an integer",
	TypeBoolean: "a boolean",
	TypeList:    "a list of strings",
}

// Describe renders a decoded TOML value for an error or a listing.
func Describe(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			parts[i] = Describe(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		return "a table"
	default:
		return fmt.Sprint(t)
	}
}
