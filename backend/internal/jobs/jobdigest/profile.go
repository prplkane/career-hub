package jobdigest

import (
	"context"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// ProfileProjection retains only the five fields read by this digest. The
// managed YAML adapter must enforce its own structural/input envelope first.
// Missing and explicit null remain distinct; strings are never coerced to bool.
// Other profile fields do not confer eligibility and are not copied into output.
// This is not validation of the complete Career Vault editor record.
func ProfileProjection(ctx context.Context, value cataloginput.Value) (EligibilityProfile, error) {
	var result EligibilityProfile
	if ctx == nil {
		return result, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if value.Kind == "null" {
		return result, nil
	}
	if value.Kind != "mapping" {
		return result, ErrInput
	}
	if len(value.Entries) > MaxItems {
		return result, ErrLimit
	}
	seen := map[string]bool{}
	for _, entry := range value.Entries {
		if err := ctx.Err(); err != nil {
			return EligibilityProfile{}, err
		}
		if entry.Key.Kind != "string" {
			continue
		}
		name := entry.Key.Text
		var field *OptionalBool
		switch name {
		case "citizenship_required_blocker":
			field = &result.CitizenshipBlocker
		case "clearance_required_blocker":
			field = &result.ClearanceBlocker
		case "requires_sponsorship_now":
			field = &result.SponsorshipNow
		case "requires_future_sponsorship":
			field = &result.SponsorshipFuture
		case "current_status":
		default:
			continue
		}
		if seen[name] {
			return EligibilityProfile{}, ErrInput
		}
		seen[name] = true
		raw := entry.Value
		if field != nil {
			field.Present = true
			if raw.Kind == "null" {
				continue
			}
			if raw.Kind != "bool" {
				return EligibilityProfile{}, ErrInput
			}
			flag := raw.Boolean
			field.Value = &flag
		} else {
			if raw.Kind == "null" {
				continue
			}
			if raw.Kind != "string" || !utf8.ValidString(raw.Text) {
				return EligibilityProfile{}, ErrInput
			}
			if len(raw.Text) > 4096 {
				return EligibilityProfile{}, ErrLimit
			}
			result.CurrentStatus = raw.Text
		}
	}
	if err := ctx.Err(); err != nil {
		return EligibilityProfile{}, err
	}
	return result, nil
}
