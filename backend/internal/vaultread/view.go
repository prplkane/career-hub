package vaultread

import (
	"context"
	"encoding/json"
)

// View is a closed application projection selector, never a filesystem path.
// Zero and unknown values are invalid and must be refused before any I/O.
type View uint8

const (
	ProfileView View = iota + 1
	EligibilityView
	EducationView
	ExperiencesView
	ProjectsView
	SkillsView
	SnapshotView
	SetupView
)

func (v View) Valid() bool { return v >= ProfileView && v <= SetupView }

func Read(ctx context.Context, root string, view View) (json.RawMessage, *Error) {
	switch view {
	case ProfileView:
		return ReadProfile(ctx, root)
	case EligibilityView:
		return ReadEligibility(ctx, root)
	case EducationView:
		return ReadEducation(ctx, root)
	case ExperiencesView:
		return ReadExperiences(ctx, root)
	case ProjectsView:
		return ReadProjects(ctx, root)
	case SkillsView:
		return ReadSkills(ctx, root)
	case SnapshotView:
		return ReadSnapshot(ctx, root)
	case SetupView:
		return ReadSetup(ctx, root)
	default:
		return nil, &Error{Stage: "view", Code: "VAULT_VIEW_INVALID"}
	}
}
