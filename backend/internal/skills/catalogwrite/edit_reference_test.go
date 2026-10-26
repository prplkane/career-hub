package catalogwrite

import (
	"careerhub.local/backend/internal/skills/cataloginput"
)

type editReferenceStep struct {
	Op              string
	Payload         map[string]any
	ID              string
	Archived, Stale bool
	Result          map[string]any
	Snapshot        map[string]any
	Files           map[string]*string
	Error           *struct {
		Class, Code, Message string
		Issues               []cataloginput.ValidationIssue
	}
}
