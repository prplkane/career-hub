package catalog

const (
	UnclassifiedGroupID = "unclassified"
	MyCustomGroupID     = "user_my_custom_skills"
	MyCustomGroupName   = "My custom skills"
)

// DefaultGroups returns a fresh copy of the source's 14 fallback core groups.
// These are used when the core group catalog is missing/empty, not appended to
// every caller-supplied catalog. My custom skills is not an implicit core group.
func DefaultGroups() []Group {
	return []Group{
		{ID: "programming_languages", Name: "Programming Languages", CascadeArchivedSkillIDs: []string{}},
		{ID: "frontend_development", Name: "Frontend Development", CascadeArchivedSkillIDs: []string{}},
		{ID: "backend_development", Name: "Backend Development", CascadeArchivedSkillIDs: []string{}},
		{ID: "databases_storage", Name: "Databases & Storage", CascadeArchivedSkillIDs: []string{}},
		{ID: "cloud_infrastructure", Name: "Cloud, Infrastructure & Operations", CascadeArchivedSkillIDs: []string{}},
		{ID: "api_integrations", Name: "APIs & Integrations", CascadeArchivedSkillIDs: []string{}},
		{ID: "data_analytics", Name: "Data & Analytics", CascadeArchivedSkillIDs: []string{}},
		{ID: "ai_machine_learning", Name: "AI & Machine Learning", CascadeArchivedSkillIDs: []string{}},
		{ID: "testing_quality", Name: "Testing & Quality", CascadeArchivedSkillIDs: []string{}},
		{ID: "security_identity", Name: "Security & Identity", CascadeArchivedSkillIDs: []string{}},
		{ID: "business_systems", Name: "Business Systems", CascadeArchivedSkillIDs: []string{}},
		{ID: "collaboration_productivity", Name: "Collaboration & Productivity", CascadeArchivedSkillIDs: []string{}},
		{ID: "architecture_delivery", Name: "Architecture & Delivery", CascadeArchivedSkillIDs: []string{}},
		{ID: UnclassifiedGroupID, Name: "Unclassified", CascadeArchivedSkillIDs: []string{}},
	}
}

// EffectiveGroupID preserves explicit-group > domain > legacy-category priority.
// Domain values and term fields have already passed the upstream model contract.
// Names are not normalized here: the source mapping is exact and case-sensitive.
func EffectiveGroupID(skill Term) string {
	if skill.PrimaryGroupID != nil && *skill.PrimaryGroupID != "" {
		return *skill.PrimaryGroupID
	}
	if skill.PrimaryDomain != nil && *skill.PrimaryDomain != "" {
		switch *skill.PrimaryDomain {
		case "ai_systems":
			return "ai_machine_learning"
		case "software_engineering", "product_and_delivery":
			return "architecture_delivery"
		case "data_and_analytics":
			return "data_analytics"
		case "cloud_and_infrastructure":
			return "cloud_infrastructure"
		case "security_and_identity":
			return "security_identity"
		case "web_and_api":
			return "api_integrations"
		case "business_systems":
			return "business_systems"
		case "other":
			return UnclassifiedGroupID
		}
		// Unreachable for a validated PrimaryDomain enum. This package does
		// not substitute a made-up category mapping for invalid domain input.
		return ""
	}
	switch skill.Category {
	case "ai", "ai_capability", "ai_developer_tool", "ai_integration_protocol", "ai_service_management", "machine_learning", "machine_learning_framework":
		return "ai_machine_learning"
	case "automation", "developer_tool", "methodology", "software_architecture":
		return "architecture_delivery"
	case "backend", "backend_framework", "object_relational_mapper":
		return "backend_development"
	case "business_intelligence", "data", "data_architecture", "data_format", "data_modeling", "data_processing_framework", "data_tool", "data_transformation_tool", "database_connectivity", "workflow_orchestration":
		return "data_analytics"
	case "business_system", "content_management_platform", "domain_skill", "platform":
		return "business_systems"
	case "certification", "custom_skill":
		return UnclassifiedGroupID
	case "ci_cd", "cloud", "cloud_platform", "container_orchestration", "deployment", "devops", "infrastructure_as_code", "observability_tool", "operating_system", "operational_practice":
		return "cloud_infrastructure"
	case "collaboration_suite", "collaboration_tool", "productivity_tool", "video_conferencing":
		return "collaboration_productivity"
	case "compliance_standard", "endpoint_management", "identity_provider", "identity_security", "networking", "password_manager", "security":
		return "security_identity"
	case "database", "database_platform", "search_engine":
		return "databases_storage"
	case "database_language", "programming_language", "scripting_language":
		return "programming_languages"
	case "frontend", "frontend_framework":
		return "frontend_development"
	case "integration", "messaging_integration":
		return "api_integrations"
	case "security_testing", "testing", "testing_methodology":
		return "testing_quality"
	default:
		return UnclassifiedGroupID
	}
}
