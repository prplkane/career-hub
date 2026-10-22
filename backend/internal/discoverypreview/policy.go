package discoverypreview

import (
	"context"

	"careerhub.local/backend/internal/cataloginput"
)

var policyClasses = [...]string{"citizenship_work_authorization", "active_clearance", "minimum_experience_years"}

// Policy validates the complete persisted policy before projecting its classes.
// Missing uses the documented defaults; malformed/present files never do.
// Schema bool/float lookalikes are rejected explicitly, unlike source == 1.
func Policy(ctx context.Context, document cataloginput.Value, present bool) (cataloginput.Value, error) {
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	result := cataloginput.Value{Kind: "mapping"}
	if !present {
		for _, name := range policyClasses {
			result.Entries = append(result.Entries, cataloginput.Entry{Key: cataloginput.Value{Kind: "string", Text: name}, Value: cataloginput.Value{Kind: "bool", Boolean: true}})
		}
		return result, nil
	}
	if document.Kind != "mapping" || len(document.Entries) != 2 {
		return cataloginput.Value{}, ErrPolicy
	}
	seen := map[string]bool{}
	var classes cataloginput.Value
	for _, entry := range document.Entries {
		if err := stopped(ctx); err != nil {
			return cataloginput.Value{}, err
		}
		if entry.Key.Kind != "string" || seen[entry.Key.Text] {
			return cataloginput.Value{}, ErrPolicy
		}
		seen[entry.Key.Text] = true
		switch entry.Key.Text {
		case "schema_version":
			if entry.Value.Kind != "int" || entry.Value.Text != "1" {
				return cataloginput.Value{}, ErrPolicy
			}
		case "classes":
			classes = entry.Value
		default:
			return cataloginput.Value{}, ErrPolicy
		}
	}
	if classes.Kind != "mapping" || len(classes.Entries) != len(policyClasses) {
		return cataloginput.Value{}, ErrPolicy
	}
	flags := map[string]bool{}
	for _, entry := range classes.Entries {
		if err := stopped(ctx); err != nil {
			return cataloginput.Value{}, err
		}
		if entry.Key.Kind != "string" || entry.Value.Kind != "bool" {
			return cataloginput.Value{}, ErrPolicy
		}
		if _, ok := flags[entry.Key.Text]; ok {
			return cataloginput.Value{}, ErrPolicy
		}
		flags[entry.Key.Text] = entry.Value.Boolean
	}
	for _, name := range policyClasses {
		enabled, ok := flags[name]
		if !ok {
			return cataloginput.Value{}, ErrPolicy
		}
		result.Entries = append(result.Entries, cataloginput.Entry{Key: cataloginput.Value{Kind: "string", Text: name}, Value: cataloginput.Value{Kind: "bool", Boolean: enabled}})
	}
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	return result, nil
}
