package catalog

type Relation struct {
	Type     string `json:"type"`
	TargetID string `json:"target_id"`
}

// Term retains every TechnologyTerm field and its explicit input-field presence.
// Upstream model adapters supply defaults and valid scalar strings/enums. FieldsSet
// affects overlay behavior but is deliberately not a serialized product field.
type Term struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Aliases        []string   `json:"aliases"`
	Category       string     `json:"category"`
	PrimaryGroupID *string    `json:"primary_group_id"`
	Parent         *string    `json:"parent"`
	Kind           string     `json:"kind"`
	PrimaryDomain  *string    `json:"primary_domain"`
	Relations      []Relation `json:"relations"`
	Archived       bool       `json:"archived"`
	CreatedVia     *string    `json:"created_via"`
	CreatedAt      *string    `json:"created_at"`
	FieldsSet      []string   `json:"-"`
}

type Group struct {
	ID                      string   `json:"id"`
	Name                    string   `json:"name"`
	Archived                bool     `json:"archived"`
	CascadeArchivedSkillIDs []string `json:"cascade_archived_skill_ids"`
}

type Notice struct {
	Kind       string `json:"kind"`
	CoreID     string `json:"core_id"`
	CustomID   string `json:"custom_id"`
	Value      string `json:"value"`
	Resolution string `json:"resolution"`
}

// Error preserves source exception class, optional registry code and message.
// Messages may contain catalog text; a diagnostic adapter must apply its own
// redaction policy before logging. Graph ValueErrors have an empty Code.
type Error struct {
	Class   string
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func cloneCatalogStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func cloneCatalogStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneTerm(value Term) Term {
	result := value
	result.Aliases = cloneCatalogStrings(value.Aliases)
	result.FieldsSet = cloneCatalogStrings(value.FieldsSet)
	result.Relations = make([]Relation, len(value.Relations))
	copy(result.Relations, value.Relations)
	result.PrimaryGroupID = cloneCatalogStringPointer(value.PrimaryGroupID)
	result.Parent = cloneCatalogStringPointer(value.Parent)
	result.PrimaryDomain = cloneCatalogStringPointer(value.PrimaryDomain)
	result.CreatedVia = cloneCatalogStringPointer(value.CreatedVia)
	result.CreatedAt = cloneCatalogStringPointer(value.CreatedAt)
	return result
}

func cloneGroup(value Group) Group {
	result := value
	result.CascadeArchivedSkillIDs = cloneCatalogStrings(value.CascadeArchivedSkillIDs)
	return result
}
