import { api } from "./ui/api.js";
import { MaterialReview } from "./features/material_review.js";
import { CLIENT_DIAGNOSTIC_EVENT } from "./ui/client_diagnostics.js";
import {
  DIAGNOSTIC_PAGES_PER_POLL,
  classifyDiagnosticReadFailure,
  parseDiagnosticPage,
} from "./ui/diagnostics_paging.js";
import { initializeConnectivity } from "./ui/connectivity.js";
import { createMonthPicker, createMonthRangePicker } from "./ui/dates.js";
import {
  createSearchableCombobox,
  createToastManager,
  escapeHtml,
  formatBytes,
  formatRequestError,
  formatTime,
  renderActionBar,
  renderLoadingState,
  renderStatusBadge,
  statusClass,
} from "./ui/primitives.js";
import {
  isJobWorkflowView,
  jobWorkflowCopy,
  renderNavigation,
} from "./navigation.js";
import {
  formatCompensationRange as formatJobCompensationRange,
  assessmentRecommendation,
  renderAssessmentSummary,
  renderJobIdentityFields,
} from "./features/jobs.js";
import {
  APPLICATION_STATUS_ORDER,
  applicationStatusLabel,
  formatApplicationDate,
  canManageApplicationMaterials,
  filterApplicationJobs,
  hasApplicationRecord,
  renderApplicationHistory,
  renderApplicationEmailUpdateRows,
  renderApplicationSessionReviewRows,
  renderApplicationLedgerRows,
} from "./features/applications.js";
import {
  firstResumePlanApprovalIssue,
  hasCurrentIntakeApprovalPreview,
  renderResumePlanEducation,
  renderResumePlanExperiences,
  renderResumePlanIssues,
  renderResumePlanProjects,
  renderResumePlanSections,
  renderResumePlanSkills,
} from "./features/resumes.js";
import {
  coverLetterIssueSummary,
  materialIssueSummary,
  renderCoverLetterEvidenceCatalog,
  renderCoverLetterEvidenceItems,
  renderMaterialIssues,
  renderResumeBlocks,
  renderResumeEducation,
  renderResumeEvidenceRefs,
  renderResumeSkills,
} from "./features/materials.js";
import {
  evidenceItemsFromInput,
  moveEvidenceItem,
  normalizedEvidenceItems,
  removeEvidenceItem,
} from "./features/evidence_items.js";
import { initDocuments, loadDocuments } from "./features/documents.js";
import {
  initDiscoveries,
  loadDiscoveries,
  setDiscoveryRouteTarget,
} from "./features/discoveries.js";
import {
  initContacts,
  loadContacts,
  openContactCandidateConfirmation,
  selectContact,
} from "./features/contacts.js";
import {
  initApplicationHelperConfiguration,
  loadApplicationHelperConfiguration,
  openApplicationHelperConfiguration,
} from "./features/application_helper.js";
import { initHelpCenter } from "./features/help.js";
import { initDesktopUpdates } from "./features/desktop_updates.js";
import { initDesktopBackups } from "./features/desktop_backups.js";
import { initDesktopAgent } from "./features/desktop_agent.js";
import { initExtensionPairing } from "./features/extension_pairing.js";

const state = {
  sources: [],
  extractions: [],
  jobs: [],
  jobsTotal: 0,
  jobsNextCursor: null,
  sourcesTotal: 0,
  sourcesNextCursor: null,
  extractionsTotal: 0,
  extractionsNextCursor: null,
  selectedJobId: null,
  jobFeedDiscoveryId: null,
  jobFeedIntent: null,
  jobAssessment: null,
  jobAssessmentLoading: false,
  jobAssessmentError: "",
  jobIntakeIdempotencyKey: "",
  jobSourcePreview: null,
  jobInstructions: null,
  jobInstructionsLoading: false,
  jobInstructionsError: "",
  jobInstructionSaving: false,
  jobEditSaving: false,
  jobIdentityEditing: false,
  jobGatesEditing: false,
  jobRequirementsEditing: false,
  jobWorkspaceTab: "prepare",
  jobApplication: null,
  jobApplicationLoading: false,
  jobApplicationError: "",
  applicationResumeLoading: false,
  applicationResumeMessageJobId: null,
  libraryResumeUploadLoading: false,
  sourceUploadCheck: null,
  sourceUploadCheckLoading: false,
  keepApprovedResumeLoadingSourceId: null,
  removeApprovedImportLoadingSourceId: null,
  resumeLibrary: [],
  resumeLibraryManagement: [],
  resumeLibraryManagementError: false,
  resumeLibraryVisibility: "active",
  resumeLibraryFocusId: null,
  resumeLibraryDigestFilter: "all",
  resumeLibrarySort: "newest",
  resumeLibraryMutationId: null,
  resumeLibraryMutationAction: null,
  applicationLedgerScope: "current",
  applicationDrawerOpen: false,
  applicationEmailUpdates: null,
  applicationEmailUpdatesNextCursor: null,
  applicationEmailUpdatesLoading: false,
  applicationEmailUpdateMutationId: null,
  applicationSessionReviews: null,
  applicationSessionReviewsLoading: false,
  applicationSessionMutationId: null,
  resumePlanEditor: null,
  resumePlanLoading: false,
  resumePlanDirty: false,
  resumePlanJobId: null,
  resumePlanAttentionBlockId: null,
  materialEditor: null,
  materialEditorLoading: false,
  materialEditorSaving: false,
  materialEditorDirty: false,
  resumeMaterialEditor: null,
  resumeMaterialLoading: false,
  resumeMaterialDirty: false,
  materialEditorJobId: null,
  materialEditorReturnView: "job-library",
  materialEditorTab: "cover-letter",
  resumeMaterialStep: "select",
  materialEvidenceSearch: "",
  coverLetterSummaries: {},
  assessmentResolutionFindingKey: "",
  assessmentResolutionEditTarget: "",
  logCursor: 0,
  logEvents: [],
  selectedRunId: null,
  logSelectionPinned: false,
  activeView: "vault",
  profileRecord: null,
  profileRevision: "",
  profileFormDirty: false,
  profileEditing: false,
  eligibilityProfile: null,
  eligibilityProfileLoading: false,
  eligibilityProfileSaving: false,
  eligibilityProfileDirty: false,
  hardBlockerPolicy: null,
  hardBlockerPolicyLoading: false,
  hardBlockerPolicySaving: false,
  hardBlockerPolicyDirty: false,
  reviewSourceId: null,
  reviewDetail: null,
  approvalPreview: null,
  selectedEntityId: null,
  vaultExperiences: [],
  vaultCategories: [],
  vaultRevision: "",
  selectedExperienceId: null,
  vaultEditorMode: "edit",
  vaultVisibility: "active",
  vaultFormDirty: false,
  vaultProofPoints: [],
  careerVaultLayout: "list",
  vaultCardDetailOpen: {
    experience: false,
    projects: false,
    education: false,
  },
  vaultProjects: [],
  projectCategories: [],
  projectExperienceOptions: [],
  projectRevision: "",
  selectedProjectId: null,
  projectEditorMode: "edit",
  projectVisibility: "active",
  projectFormDirty: false,
  manualSkills: {
    experience: { ids: [], pending: [] },
    project: { ids: [], pending: [] },
  },
  skillsRegistry: null,
  skillsHubMode: "groups",
  skillsRegistryLayout: "list",
  registryVisibility: "active",
  registryGroupSort: "name_asc",
  registrySkillVisibility: "active",
  registrySkillSort: "name_asc",
  registryCardDetailOpen: false,
  selectedRegistryGroupId: null,
  selectedRegistrySkillId: null,
  registryGroupEditing: false,
  skillQuickMode: "skill",
  skillQuickNames: [],
  skillQuickSaving: false,
  skillQuickContext: null,
  competencyExplorer: null,
  historicalCompetencyExplorer: null,
  selectedCompetencyId: null,
  demandGaps: null,
  demandGapsCoverageFilter: "",
  demandGapsDrawerOpen: false,
  demandGapsRelatedJobsExpanded: false,
  selectedDemandGapId: null,
  vaultSchools: [],
  vaultCertifications: [],
  educationRevision: "",
  educationType: "school",
  selectedEducationId: null,
  educationEditorMode: "edit",
  educationVisibility: "active",
  educationFormDirty: false,
  reviewDirty: false,
  blockFilter: "all",
  reviewInspectorOpen: false,
  runtimeInfo: null,
};

const MAX_LOG_EVENTS = 1_000;
const SKILLS_HUB_MODE_KEY = "careerHub.skillsHubMode";
const SKILLS_REGISTRY_LAYOUT_KEY = "careerHub.skillsRegistryLayout";
const CAREER_VAULT_LAYOUT_KEY = "careerHub.careerVaultLayout";
const DEMAND_GAPS_PERIOD_KEY = "careerHub.demandGapsPeriod";
const viewScrollPositions = new Map();
const mobileNavigationMedia = window.matchMedia("(max-width: 820px)");
let vaultSnapshot = null;
let vaultSnapshotPromise = null;
let jobsLoaded = false;
let resumeCollectionsLoaded = false;
let resumeLibraryManagementLoaded = false;
let competencyExplorerLoaded = false;
let historicalCompetencyExplorerLoaded = false;
let historicalCompetencyExplorerPromise = null;
let demandGapsLoaded = false;
let demandGapsLoadEpoch = 0;
let demandGapDrawerReturnFocus = null;
let diagnosticsPollTimer = null;
let diagnosticsPollInFlight = false;
let diagnosticsPollPaused = false;
let diagnosticsRetryAfter = 0;
let reviewLoadEpoch = 0;


const elements = {
  primaryNavigation: document.querySelector("#primary-navigation"),
  sidebar: document.querySelector(".sidebar"),
  mobileNavigationToggle: document.querySelector("#mobile-navigation-toggle"),
  mobileNavigationClose: document.querySelector("#mobile-navigation-close"),
  mobileAddButton: document.querySelector(".mobile-add-button"),
  sidebarBackdrop: document.querySelector("#sidebar-backdrop"),
  runtimeStorageNote: document.querySelector("#runtime-storage-note"),
  runtimeDataProfile: document.querySelector("#runtime-data-profile"),
  runtimeDataRoot: document.querySelector("#runtime-data-root"),
  emailAppPasswordHelp: document.querySelector("#email-app-password-help"),
  emailSecurityHelp: document.querySelector("#email-security-help"),
  documentSecurityCopy: document.querySelector("#document-security-copy"),
  profileEditorTitle: document.querySelector("#profile-editor-title"),
  profileEditorShell: document.querySelector(".profile-editor-shell"),
  editProfileButton: document.querySelector("#edit-profile-button"),
  profileForm: document.querySelector("#profile-form"),
  profileName: document.querySelector("#profile-name"),
  profileGivenName: document.querySelector("#profile-given-name"),
  profileFamilyName: document.querySelector("#profile-family-name"),
  profilePreferredName: document.querySelector("#profile-preferred-name"),
  profileMajor: document.querySelector("#profile-major"),
  profileCurrentLocation: document.querySelector("#profile-current-location"),
  profileTargetLocation: document.querySelector("#profile-target-location"),
  profileLocationFlexibility: document.querySelector("#profile-location-flexibility"),
  profileWorkAuthorization: document.querySelector("#profile-work-authorization"),
  profileWorkAuthorizationOtherField: document.querySelector("#profile-work-authorization-other-field"),
  profileWorkAuthorizationOther: document.querySelector("#profile-work-authorization-other"),
  profileEmail: document.querySelector("#profile-email"),
  profilePhone: document.querySelector("#profile-phone"),
  profileAddress: document.querySelector("#profile-address"),
  profileAddressLine1: document.querySelector("#profile-address-line1"),
  profileAddressLine2: document.querySelector("#profile-address-line2"),
  profileCity: document.querySelector("#profile-city"),
  profileRegion: document.querySelector("#profile-region"),
  profilePostalCode: document.querySelector("#profile-postal-code"),
  profileCountry: document.querySelector("#profile-country"),
  profileLinkedin: document.querySelector("#profile-linkedin"),
  profileGithub: document.querySelector("#profile-github"),
  profileWebsite: document.querySelector("#profile-website"),
  profileRoleTargets: document.querySelector("#profile-role-targets"),
  profileRoleTargetTags: document.querySelector("#profile-role-target-tags"),
  eligibilityCurrentStatus: document.querySelector("#eligibility-current-status"),
  eligibilityWorkAuthorization: document.querySelector("#eligibility-work-authorization"),
  eligibilitySponsorshipNow: document.querySelector("#eligibility-sponsorship-now"),
  eligibilitySponsorshipFuture: document.querySelector("#eligibility-sponsorship-future"),
  eligibilityCitizenshipConflict: document.querySelector("#eligibility-citizenship-conflict"),
  eligibilityClearanceConflict: document.querySelector("#eligibility-clearance-conflict"),
  eligibilityStemOpt: document.querySelector("#eligibility-stem-opt"),
  eligibilityProfileMessage: document.querySelector("#eligibility-profile-message"),
  saveEligibilityProfileButton: document.querySelector("#save-eligibility-profile-button"),
  hardBlockerWorkAuthorization: document.querySelector("#hard-blocker-work-authorization"),
  hardBlockerClearance: document.querySelector("#hard-blocker-clearance"),
  hardBlockerExperience: document.querySelector("#hard-blocker-experience"),
  hardBlockerPolicyMessage: document.querySelector("#hard-blocker-policy-message"),
  saveHardBlockerPolicyButton: document.querySelector("#save-hard-blocker-policy-button"),
  profileMessage: document.querySelector("#profile-message"),
  cancelProfileButton: document.querySelector("#cancel-profile-button"),
  saveProfileButton: document.querySelector("#save-profile-button"),
  vaultSearch: document.querySelector("#vault-search"),
  vaultCategoryFilter: document.querySelector("#vault-category-filter"),
  refreshVaultButton: document.querySelector("#refresh-vault-button"),
  vaultArchivedFilter: document.querySelector("#vault-archived-filter"),
  vaultArchivedCount: document.querySelector("#vault-archived-count"),
  newExperienceButton: document.querySelector("#new-experience-button"),
  vaultExperienceCount: document.querySelector("#vault-experience-count"),
  vaultExperienceList: document.querySelector("#vault-experience-list"),
  vaultEditorSource: document.querySelector("#vault-editor-source"),
  vaultEditorTitle: document.querySelector("#vault-editor-title"),
  vaultEditorId: document.querySelector("#vault-editor-id"),
  vaultExperienceForm: document.querySelector("#vault-experience-form"),
  vaultCompany: document.querySelector("#vault-company"),
  vaultRole: document.querySelector("#vault-role"),
  vaultDateRange: document.querySelector("#vault-date-range"),
  vaultCategory: document.querySelector("#vault-category"),
  vaultCategoryOptions: document.querySelector("#vault-category-options"),
  vaultSkillPills: document.querySelector("#vault-skill-pills"),
  vaultSkillInput: document.querySelector("#vault-skill-input"),
  vaultSkillOptions: document.querySelector("#vault-skill-options"),
  vaultPendingSkills: document.querySelector("#vault-pending-skills"),
  vaultSkillCount: document.querySelector("#vault-skill-count"),
  addVaultSkills: document.querySelector("#vault-skills-add"),
  vaultProofPointCount: document.querySelector("#vault-proof-point-count"),
  vaultProofPointList: document.querySelector("#vault-proof-point-list"),
  vaultProofPointInput: document.querySelector("#vault-proof-point-input"),
  addVaultProofPoint: document.querySelector("#add-vault-proof-point"),
  vaultMessage: document.querySelector("#vault-message"),
  archiveVaultButton: document.querySelector("#archive-vault-button"),
  cancelVaultButton: document.querySelector("#cancel-vault-button"),
  saveVaultButton: document.querySelector("#save-vault-button"),
  projectSearch: document.querySelector("#project-search"),
  projectCategoryFilter: document.querySelector("#project-category-filter"),
  refreshProjectsButton: document.querySelector("#refresh-projects-button"),
  projectArchivedFilter: document.querySelector("#project-archived-filter"),
  projectArchivedCount: document.querySelector("#project-archived-count"),
  newProjectButton: document.querySelector("#new-project-button"),
  projectRecordCount: document.querySelector("#project-record-count"),
  projectRecordList: document.querySelector("#project-record-list"),
  projectEditorSource: document.querySelector("#project-editor-source"),
  projectEditorTitle: document.querySelector("#project-editor-title"),
  projectEditorId: document.querySelector("#project-editor-id"),
  projectForm: document.querySelector("#project-form"),
  projectName: document.querySelector("#project-name"),
  projectCategory: document.querySelector("#project-category"),
  projectCategoryOptions: document.querySelector("#project-category-options"),
  projectSummary: document.querySelector("#project-summary"),
  projectDateRange: document.querySelector("#project-date-range"),
  projectSkillPills: document.querySelector("#project-skill-pills"),
  projectSkillInput: document.querySelector("#project-skill-input"),
  projectSkillOptions: document.querySelector("#project-skill-options"),
  projectPendingSkills: document.querySelector("#project-pending-skills"),
  projectSkillCount: document.querySelector("#project-skill-count"),
  addProjectSkills: document.querySelector("#project-skills-add"),
  projectRelatedExperience: document.querySelector("#project-related-experience"),
  projectProofPoints: document.querySelector("#project-proof-points"),
  projectMessage: document.querySelector("#project-message"),
  archiveProjectButton: document.querySelector("#archive-project-button"),
  cancelProjectButton: document.querySelector("#cancel-project-button"),
  saveProjectButton: document.querySelector("#save-project-button"),
  skillsRegistryGrid: document.querySelector("#skills-registry-grid"),
  skillsRegistryPanel: document.querySelector(".skills-registry-panel"),
  skillsRegistryLayoutButtons: [...document.querySelectorAll(".view-mode-switch [data-registry-layout]")],
  skillsRegistryGroupCount: document.querySelector("#skills-registry-group-count"),
  skillsRegistrySearch: document.querySelector("#skills-registry-search"),
  skillsRegistrySort: document.querySelector("#skills-registry-sort"),
  skillsRegistryArchivedFilter: document.querySelector("#skills-registry-archived-filter"),
  skillsRegistryArchivedCount: document.querySelector("#skills-registry-archived-count"),
  skillsRegistryDetail: document.querySelector("#skills-registry-detail"),
  skillsRegistryMessage: document.querySelector("#skills-registry-message"),
  skillsHubModeButtons: [...document.querySelectorAll("[data-skills-hub-mode]")],
  skillsHubSurfaces: [...document.querySelectorAll("[data-skills-hub-surface]")],
  competencySummary: document.querySelector("#competency-summary"),
  competencySearch: document.querySelector("#competency-search"),
  competencyGroupFilter: document.querySelector("#competency-group-filter"),
  competencyOriginFilter: document.querySelector("#competency-origin-filter"),
  competencyEvidenceFilter: document.querySelector("#competency-evidence-filter"),
  competencyDomainFilter: document.querySelector("#competency-domain-filter"),
  competencyKindFilter: document.querySelector("#competency-kind-filter"),
  competencyList: document.querySelector("#competency-list"),
  competencyResultCount: document.querySelector("#competency-result-count"),
  competencyDetail: document.querySelector("#competency-detail"),
  competencyMessage: document.querySelector("#competency-message"),
  demandGapsWorkflow: document.querySelector("#demand-gaps-workflow"),
  demandGapsHeaderControls: document.querySelector("#demand-gaps-header-controls"),
  demandGapsJobCount: document.querySelector("#demand-gaps-job-count"),
  demandGapsSkillCount: document.querySelector("#demand-gaps-skill-count"),
  demandGapsSupportedCount: document.querySelector("#demand-gaps-supported-count"),
  demandGapsThinCount: document.querySelector("#demand-gaps-thin-count"),
  demandGapsMissingCount: document.querySelector("#demand-gaps-missing-count"),
  demandGapsAllCount: document.querySelector("#demand-gaps-all-count"),
  demandGapsCoverageBar: document.querySelector("#demand-gaps-coverage-bar"),
  demandGapsCoverageLegend: document.querySelector("#demand-gaps-coverage-legend"),
  demandGapsTopDemand: document.querySelector("#demand-gaps-top-demand"),
  demandGapsPriority: document.querySelector("#demand-gaps-priority"),
  demandGapsPeriodLabel: document.querySelector("#demand-gaps-period-label"),
  demandGapsPeriodPreset: document.querySelector("#demand-gaps-period-preset"),
  demandGapsPeriodForm: document.querySelector("#demand-gaps-period-form"),
  demandGapsFrom: document.querySelector("#demand-gaps-from"),
  demandGapsTo: document.querySelector("#demand-gaps-to"),
  demandGapsFilterMenu: document.querySelector("#demand-gaps-filter-menu"),
  demandGapsFilterSummaryLabel: document.querySelector("#demand-gaps-filter-summary-label"),
  demandGapsDomain: document.querySelector("#demand-gaps-domain"),
  demandGapsRequirement: document.querySelector("#demand-gaps-requirement"),
  demandGapsSort: document.querySelector("#demand-gaps-sort"),
  demandGapsSearch: document.querySelector("#demand-gaps-search"),
  demandGapsCoverageButtons: [...document.querySelectorAll("[data-demand-gap-coverage]")],
  demandGapsRows: document.querySelector("#demand-gaps-rows"),
  demandGapsResultCount: document.querySelector("#demand-gaps-result-count"),
  demandGapsSourceNote: document.querySelector("#demand-gaps-source-note"),
  demandGapsMessage: document.querySelector("#demand-gaps-message"),
  demandGapDrawerBackdrop: document.querySelector("#demand-gap-drawer-backdrop"),
  demandGapDrawer: document.querySelector("#demand-gap-drawer"),
  demandGapDrawerTitle: document.querySelector("#demand-gap-drawer-title"),
  demandGapDrawerStatus: document.querySelector("#demand-gap-drawer-status"),
  demandGapDrawerRequired: document.querySelector("#demand-gap-drawer-required"),
  demandGapDrawerPreferred: document.querySelector("#demand-gap-drawer-preferred"),
  demandGapDrawerJobCount: document.querySelector("#demand-gap-drawer-job-count"),
  demandGapRelatedJobs: document.querySelector("#demand-gap-related-jobs"),
  closeDemandGapDrawer: document.querySelector("#close-demand-gap-drawer"),
  openDemandGapSkill: document.querySelector("#open-demand-gap-skill"),
  toggleDemandGapJobs: document.querySelector("#toggle-demand-gap-jobs"),
  careerVaultLayoutButtons: [...document.querySelectorAll(".vault-layout-switch [data-vault-layout]")],
  careerVaultWorkspaces: [...document.querySelectorAll(".vault-workspace[data-vault-surface]")],
  skillQuickDialog: document.querySelector("#skill-quick-dialog"),
  skillQuickForm: document.querySelector("#skill-quick-form"),
  skillQuickTitle: document.querySelector("#skill-quick-title"),
  skillQuickNameLabel: document.querySelector("#skill-quick-name-label"),
  skillQuickName: document.querySelector("#skill-quick-name"),
  skillQuickPills: document.querySelector("#skill-quick-pills"),
  skillQuickNameHelp: document.querySelector("#skill-quick-name-help"),
  skillQuickGroupField: document.querySelector("#skill-quick-group-field"),
  skillQuickGroup: document.querySelector("#skill-quick-group"),
  skillQuickAliasesField: document.querySelector("#skill-quick-aliases-field"),
  skillQuickAliases: document.querySelector("#skill-quick-aliases"),
  skillQuickMessage: document.querySelector("#skill-quick-message"),
  saveSkillQuickAction: document.querySelector("#save-skill-quick-action"),
  closeSkillQuickDialog: document.querySelector("#close-skill-quick-dialog"),
  cancelSkillQuickDialog: document.querySelector("#cancel-skill-quick-dialog"),
  educationSearch: document.querySelector("#education-search"),
  educationSchoolFilter: document.querySelector("#education-school-filter"),
  educationCertificationFilter: document.querySelector("#education-certification-filter"),
  educationSchoolCount: document.querySelector("#education-school-count"),
  educationCertificationCount: document.querySelector("#education-certification-count"),
  educationArchivedFilter: document.querySelector("#education-archived-filter"),
  educationArchivedCount: document.querySelector("#education-archived-count"),
  refreshEducationButton: document.querySelector("#refresh-education-button"),
  newEducationButton: document.querySelector("#new-education-button"),
  educationListTitle: document.querySelector("#education-list-title"),
  educationRecordCount: document.querySelector("#education-record-count"),
  educationRecordList: document.querySelector("#education-record-list"),
  educationEditorSource: document.querySelector("#education-editor-source"),
  educationEditorTitle: document.querySelector("#education-editor-title"),
  educationEditorId: document.querySelector("#education-editor-id"),
  educationForm: document.querySelector("#education-form"),
  schoolFields: document.querySelector("#school-fields"),
  educationInstitution: document.querySelector("#education-institution"),
  educationDegree: document.querySelector("#education-degree"),
  educationMajor: document.querySelector("#education-major"),
  educationLocation: document.querySelector("#education-location"),
  educationDateRange: document.querySelector("#education-date-range"),
  educationGraduationMonth: document.querySelector("#education-graduation-month"),
  educationGpa: document.querySelector("#education-gpa"),
  educationStatus: document.querySelector("#education-status"),
  educationHonors: document.querySelector("#education-honors"),
  certificationFields: document.querySelector("#certification-fields"),
  certificationName: document.querySelector("#certification-name"),
  certificationCredential: document.querySelector("#certification-credential"),
  certificationCompletedMonth: document.querySelector("#certification-completed-month"),
  certificationProofPoints: document.querySelector("#certification-proof-points"),
  educationMessage: document.querySelector("#education-message"),
  archiveEducationButton: document.querySelector("#archive-education-button"),
  cancelEducationButton: document.querySelector("#cancel-education-button"),
  saveEducationButton: document.querySelector("#save-education-button"),
  resumeIntentInputs: [...document.querySelectorAll('input[name="resume-intent"]')],
  resumeVaultIntake: document.querySelector("#resume-vault-intake"),
  resumeApplicationIntent: document.querySelector("#resume-application-intent"),
  libraryResumeFile: document.querySelector("#library-resume-file"),
  libraryResumeDisplayName: document.querySelector("#library-resume-display-name"),
  saveLibraryResumeButton: document.querySelector("#save-library-resume-button"),
  libraryResumeMessage: document.querySelector("#library-resume-message"),
  sourceSelect: document.querySelector("#source-select"),
  sourceUpload: document.querySelector("#source-upload"),
  uploadButton: document.querySelector("#upload-button"),
  uploadMessage: document.querySelector("#upload-message"),
  runButton: document.querySelector("#run-button"),
  resumeImportsInProgress: document.querySelector("#resume-imports-in-progress"),
  resumeImportsInProgressCount: document.querySelector("#resume-imports-in-progress-count"),
  resumeImportsInProgressList: document.querySelector("#resume-imports-in-progress-list"),
  loadMoreSourcesButton: document.querySelector("#load-more-sources-button"),
  overwriteInput: document.querySelector("#overwrite-input"),
  runMessage: document.querySelector("#run-message"),
  jobCompany: document.querySelector("#job-company"),
  jobTitle: document.querySelector("#job-title"),
  jobSourceUrl: document.querySelector("#job-source-url"),
  jobSourceName: document.querySelector("#job-source-name"),
  jobPostingId: document.querySelector("#job-posting-id"),
  jobSourceFile: document.querySelector("#job-source-file"),
  jobSourceText: document.querySelector("#job-source-text"),
  jobCharacterCount: document.querySelector("#job-character-count"),
  createJobButton: document.querySelector("#create-job-button"),
  createAppliedJobButton: document.querySelector("#create-applied-job-button"),
  refreshJobsButton: document.querySelector("#refresh-jobs-button"),
  loadMoreJobsButton: document.querySelector("#load-more-jobs-button"),
  jobMessage: document.querySelector("#job-message"),
  resumeLibraryCount: document.querySelector("#resume-library-count"),
  resumeLibraryPanel: document.querySelector(".resume-library-panel"),
  resumeLibrarySearch: document.querySelector("#resume-library-search"),
  resumeLibraryDigestFilter: document.querySelector("#resume-library-digest-filter"),
  resumeLibrarySort: document.querySelector("#resume-library-sort"),
  resumeLibraryArchivedFilter: document.querySelector("#resume-library-archived-filter"),
  resumeLibraryArchivedCount: document.querySelector("#resume-library-archived-count"),
  resumeLibraryRecordList: document.querySelector("#resume-library-record-list"),
  resumeLibraryMessage: document.querySelector("#resume-library-message"),
  resumeLibraryAddResumeButton: document.querySelector("#resume-library-add-resume-button"),
  jobDraftSelect: document.querySelector("#job-draft-select"),
  jobDraftCombobox: document.querySelector("#job-draft-combobox"),
  jobSavedCount: document.querySelector("#job-saved-count"),
  jobSelectionMeta: document.querySelector("#job-selection-meta"),
  jobLibraryApplicationBar: document.querySelector("#job-library-application-bar"),
  jobLibraryMessage: document.querySelector("#job-library-message"),
  jobWorkspaceMaterials: document.querySelector("#job-workspace-materials"),
  jobWorkspaceMaterialsStatus: document.querySelector("#job-workspace-materials-status"),
  jobWorkspaceTabButtons: [...document.querySelectorAll("[data-job-workspace-tab]")],
  jobOverviewLayout: document.querySelector(".job-overview-layout"),
  jobDigestColumns: document.querySelector(".job-digest-columns"),
  jobDetailGrid: document.querySelector(".job-detail-grid"),
  loadJobSourceButton: document.querySelector("#load-job-source-button"),
  jobDigestSourceLink: document.querySelector("#job-digest-source-link"),
  jobSourcePreview: document.querySelector("#job-source-preview"),
  jobSourcePreviewMeta: document.querySelector("#job-source-preview-meta"),
  jobSourcePreviewText: document.querySelector("#job-source-preview-text"),
  jobInstructionChecklist: document.querySelector("#job-instruction-checklist"),
  jobInstructionCount: document.querySelector("#job-instruction-count"),
  jobInstructionList: document.querySelector("#job-instruction-list"),
  jobInstructionManual: document.querySelector("#job-instruction-manual"),
  jobInstructionCategory: document.querySelector("#job-instruction-category"),
  addSelectedJobInstruction: document.querySelector("#add-selected-job-instruction"),
  jobInstructionStale: document.querySelector("#job-instruction-stale"),
  jobInstructionMessage: document.querySelector("#job-instruction-message"),
  jobContactCandidates: document.querySelector("#job-contact-candidates"),
  jobContactCandidateCount: document.querySelector("#job-contact-candidate-count"),
  jobContactCandidateList: document.querySelector("#job-contact-candidate-list"),
  reanalyzeJobButton: document.querySelector("#reanalyze-job-button"),
  jobWorkflowTitle: document.querySelector("#job-workflow-title"),
  jobWorkflowDescription: document.querySelector("#job-workflow-description"),
  jobWorkflowState: document.querySelector("#job-workflow-state"),
  jobIntakeWorkflow: document.querySelector("#job-intake-workflow"),
  jobFeedWorkflow: document.querySelector("#job-feed-workflow"),
  jobLibraryWorkflow: document.querySelector("#job-library-workflow"),
  applicationLedgerWorkflow: document.querySelector("#application-ledger-workflow"),
  applicationLedgerSearch: document.querySelector("#application-ledger-search"),

  applicationLedgerStatusFilter: document.querySelector("#application-ledger-status-filter"),
  refreshApplicationsButton: document.querySelector("#refresh-applications-button"),
  applicationLedgerCount: document.querySelector("#application-ledger-count"),
  applicationLedgerList: document.querySelector("#application-ledger-list"),
  applicationLedgerMessage: document.querySelector("#application-ledger-message"),
  applicationEmailUpdates: document.querySelector("#application-email-updates"),
  applicationEmailUpdatesCount: document.querySelector("#application-email-updates-count"),
  applicationEmailUpdatesList: document.querySelector("#application-email-updates-list"),
  applicationEmailUpdatesMore: document.querySelector("#application-email-updates-more"),
  applicationSessionReviews: document.querySelector("#application-session-reviews"),
  applicationSessionReviewsCount: document.querySelector("#application-session-reviews-count"),
  applicationSessionReviewList: document.querySelector("#application-session-review-list"),
  applicationDrawerBackdrop: document.querySelector("#application-drawer-backdrop"),
  closeApplicationDrawer: document.querySelector("#close-application-drawer"),
  jobApplicationSection: document.querySelector("#job-application-section"),
  applicationDetailTitle: document.querySelector("#application-detail-title"),
  applicationDetailMeta: document.querySelector("#application-detail-meta"),
  applicationSourceLink: document.querySelector("#application-source-link"),
  openApplicationJobButton: document.querySelector("#open-application-job-button"),
  applicationResumeMaterialState: document.querySelector("#application-resume-material-state"),
  applicationResumeMaterialGuidance: document.querySelector("#application-resume-material-guidance"),
  openResumeMaterialButton: document.querySelector("#open-resume-material-button"),
  applicationCoverLetterState: document.querySelector("#application-cover-letter-state"),
  applicationCoverLetterGuidance: document.querySelector("#application-cover-letter-guidance"),
  openCoverLetterButton: document.querySelector("#open-cover-letter-button"),
  applicationCurrentStatus: document.querySelector("#application-current-status"),
  applicationSummaryStatus: document.querySelector("#application-summary-status"),
  applicationAppliedDate: document.querySelector("#application-applied-date"),
  applicationAssessmentSummary: document.querySelector("#application-assessment-summary"),
  applicationSubmittedResumeSummary: document.querySelector("#application-submitted-resume-summary"),
  openSubmittedResumeSummary: document.querySelector("#open-submitted-resume-summary"),
  applicationAssessmentDetail: document.querySelector("#application-assessment-detail"),
  applicationSessionState: document.querySelector("#application-session-state"),
  applicationSessionGuidance: document.querySelector("#application-session-guidance"),
  startApplicationSessionButton: document.querySelector("#start-application-session-button"),
  openApplicationSessionPosting: document.querySelector("#open-application-session-posting"),
  cancelApplicationSessionButton: document.querySelector("#cancel-application-session-button"),
  applicationResumeState: document.querySelector("#application-resume-state"),
  applicationResumeCurrent: document.querySelector("#application-resume-current"),
  applicationResumeLabel: document.querySelector("#application-resume-label"),
  applicationResumeMeta: document.querySelector("#application-resume-meta"),
  openApplicationResume: document.querySelector("#open-application-resume"),
  applicationResumeCombobox: document.querySelector("#application-resume-combobox"),
  clearApplicationResumeSelectionButton: document.querySelector("#clear-application-resume-selection"),
  recordSubmittedResumeButton: document.querySelector("#record-submitted-resume-button"),
  applicationResumeFile: document.querySelector("#application-resume-file"),
  applicationResumeDisplayName: document.querySelector("#application-resume-display-name"),
  attachApplicationResumeButton: document.querySelector("#attach-application-resume-button"),
  applicationResumeMessage: document.querySelector("#application-resume-message"),

  applicationStatusForm: document.querySelector("#application-status-form"),
  applicationStatusSelect: document.querySelector("#application-status-select"),
  applicationStatusDate: document.querySelector("#application-status-date"),
  applicationStatusNote: document.querySelector("#application-status-note"),
  saveApplicationStatusButton: document.querySelector("#save-application-status-button"),
  applicationMessage: document.querySelector("#application-message"),
  applicationHistoryCount: document.querySelector("#application-history-count"),
  applicationHistoryList: document.querySelector("#application-history-list"),
  resumePlanWorkspace: document.querySelector("#resume-plan-workspace"),
  resumePlanTitle: document.querySelector("#resume-plan-title"),
  resumePlanMeta: document.querySelector("#resume-plan-meta"),
  resumePlanStatus: document.querySelector("#resume-plan-status"),
  resumePlanRevision: document.querySelector("#resume-plan-revision"),
  resumePlanPages: document.querySelector("#resume-plan-pages"),
  saveResumePlanButton: document.querySelector("#save-resume-plan-button"),
  approveResumePlanButton: document.querySelector("#approve-resume-plan-button"),
  resumePlanMessage: document.querySelector("#resume-plan-message"),
  resumePlanValidation: document.querySelector("#resume-plan-validation"),
  resumePlanIssueCount: document.querySelector("#resume-plan-issue-count"),
  resumePlanIssues: document.querySelector("#resume-plan-issues"),
  resumePlanSectionList: document.querySelector("#resume-plan-section-list"),
  resumePlanExperienceCount: document.querySelector("#resume-plan-experience-count"),
  resumePlanExperienceList: document.querySelector("#resume-plan-experience-list"),
  resumePlanProjectCount: document.querySelector("#resume-plan-project-count"),
  resumePlanProjectSection: document.querySelector("#resume-plan-project-section"),
  resumePlanProjectList: document.querySelector("#resume-plan-project-list"),
  resumePlanSkillCount: document.querySelector("#resume-plan-skill-count"),
  resumePlanSkillList: document.querySelector("#resume-plan-skill-list"),
  resumePlanEducationCount: document.querySelector("#resume-plan-education-count"),
  resumePlanEducationList: document.querySelector("#resume-plan-education-list"),
  materialEditorBackButton: document.querySelector("#material-editor-back-button"),
  materialEditorTitle: document.querySelector("#material-editor-title"),
  materialEditorMeta: document.querySelector("#material-editor-meta"),
  materialEditorStatus: document.querySelector("#material-editor-status"),
  materialEditorRevision: document.querySelector("#material-editor-revision"),
  materialEditorMessage: document.querySelector("#material-editor-message"),
  materialTabButtons: [...document.querySelectorAll("[data-material-tab]")],
  materialResumePanel: document.querySelector("#material-resume-panel"),
  resumeMaterialStepButtons: [...document.querySelectorAll("[data-resume-material-step]")],
  resumeMaterialStepBack: document.querySelector("#resume-material-step-back"),
  resumeMaterialStepNext: document.querySelector("#resume-material-step-next"),
  materialResumeContext: document.querySelector("#material-resume-panel > .material-resume-context"),
  resumeMaterialToolbar: document.querySelector("#material-resume-panel > .material-editor-toolbar"),
  resumeMaterialPreview: document.querySelector("#resume-material-preview"),
  resumeMaterialExportBar: document.querySelector("#material-resume-panel > .material-export-bar"),
  resumeMaterialScopeNote: document.querySelector("#material-resume-panel > .material-scope-note"),
  materialCoverLetterPanel: document.querySelector("#material-cover-letter-panel"),
  materialReturnToResumeSelection: document.querySelector("#material-return-to-resume-selection"),
  materialSelectedResumeName: document.querySelector("#material-selected-resume-name"),
  materialSelectedResumeMeta: document.querySelector("#material-selected-resume-meta"),
  copyResumeAiCommand: document.querySelector("#copy-resume-ai-command"),
  saveResumeMaterialButton: document.querySelector("#save-resume-material-button"),
  approveResumeMaterialButton: document.querySelector("#approve-resume-material-button"),
  resumeMaterialValidation: document.querySelector("#resume-material-validation"),
  resumeMaterialIssueCount: document.querySelector("#resume-material-issue-count"),
  resumeMaterialIssues: document.querySelector("#resume-material-issues"),
  rebuildResumeMaterialButton: document.querySelector("#rebuild-resume-material-button"),
  resumeMaterialForm: document.querySelector("#resume-material-form"),
  resumeMaterialName: document.querySelector("#resume-material-name"),
  resumeMaterialEmail: document.querySelector("#resume-material-email"),
  resumeMaterialLocation: document.querySelector("#resume-material-location"),
  resumeMaterialPhone: document.querySelector("#resume-material-phone"),
  resumeMaterialLinkedin: document.querySelector("#resume-material-linkedin"),
  resumeMaterialGithub: document.querySelector("#resume-material-github"),
  resumeMaterialWebsite: document.querySelector("#resume-material-website"),
  resumeMaterialSummarySection: document.querySelector("#resume-material-summary-section"),
  resumeMaterialSummary: document.querySelector("#resume-material-summary"),
  resumeMaterialSummaryEvidence: document.querySelector("#resume-material-summary-evidence"),
  resumeMaterialExperiences: document.querySelector("#resume-material-experiences"),
  resumeMaterialProjectsSection: document.querySelector("#resume-material-projects-section"),
  resumeMaterialProjects: document.querySelector("#resume-material-projects"),
  resumeMaterialSkills: document.querySelector("#resume-material-skills"),
  resumeMaterialEducation: document.querySelector("#resume-material-education"),
  resumeMaterialPreviewText: document.querySelector("#resume-material-preview-text"),
  resumeMaterialExportNote: document.querySelector("#resume-material-export-note"),
  resumeMaterialExportButtons: [...document.querySelectorAll("[data-resume-material-export]")],
  copyCoverLetterAiCommand: document.querySelector("#copy-cover-letter-ai-command"),
  coverLetterSaveState: document.querySelector("#cover-letter-save-state"),
  coverLetterValidation: document.querySelector("#cover-letter-validation"),
  coverLetterIssueCount: document.querySelector("#cover-letter-issue-count"),
  coverLetterIssues: document.querySelector("#cover-letter-issues"),
  refreshCoverLetterSources: document.querySelector("#refresh-cover-letter-sources"),
  coverLetterForm: document.querySelector("#cover-letter-form"),
  coverLetterName: document.querySelector("#cover-letter-name"),
  coverLetterEmail: document.querySelector("#cover-letter-email"),
  coverLetterLocation: document.querySelector("#cover-letter-location"),
  coverLetterPhone: document.querySelector("#cover-letter-phone"),
  coverLetterLinkedin: document.querySelector("#cover-letter-linkedin"),
  coverLetterGithub: document.querySelector("#cover-letter-github"),
  coverLetterWebsite: document.querySelector("#cover-letter-website"),
  coverLetterDate: document.querySelector("#cover-letter-date"),
  coverLetterCompany: document.querySelector("#cover-letter-company"),
  coverLetterRole: document.querySelector("#cover-letter-role"),
  coverLetterSalutation: document.querySelector("#cover-letter-salutation"),
  coverLetterOpening: document.querySelector("#cover-letter-opening"),
  coverLetterLeadIn: document.querySelector("#cover-letter-lead-in"),
  coverLetterEvidenceSearch: document.querySelector("#cover-letter-evidence-search"),
  coverLetterEvidenceCatalog: document.querySelector("#cover-letter-evidence-catalog"),
  coverLetterEvidenceItems: document.querySelector("#cover-letter-evidence-items"),
  addCoverLetterEvidenceButton: document.querySelector("#add-cover-letter-evidence-button"),
  coverLetterClosing: document.querySelector("#cover-letter-closing"),
  coverLetterSignOff: document.querySelector("#cover-letter-sign-off"),
  coverLetterSignature: document.querySelector("#cover-letter-signature"),
  coverLetterCredential: document.querySelector("#cover-letter-credential"),
  coverLetterPreview: document.querySelector("#cover-letter-preview"),
  coverLetterPreviewText: document.querySelector("#cover-letter-preview-text"),
  coverLetterExportNote: document.querySelector("#cover-letter-export-note"),
  coverLetterExportButtons: [...document.querySelectorAll("[data-cover-letter-export]")],
  jobDigestSection: document.querySelector("#job-digest-section"),
  jobDigestTitle: document.querySelector("#job-digest-title"),
  jobDigestStatus: document.querySelector("#job-digest-status"),
  editJobDetailsButton: document.querySelector("#edit-job-details-button"),
  jobEditMessage: document.querySelector("#job-edit-message"),
  cancelJobEditButton: document.querySelector("#cancel-job-edit-button"),
  saveJobEditButton: document.querySelector("#save-job-edit-button"),
  assessmentResolutionDialog: document.querySelector("#assessment-resolution-dialog"),
  assessmentResolutionForm: document.querySelector("#assessment-resolution-form"),
  assessmentResolutionFinding: document.querySelector("#assessment-resolution-finding"),
  assessmentResolutionValues: [...document.querySelectorAll('input[name="assessment-resolution"]')],
  assessmentResolutionNote: document.querySelector("#assessment-resolution-note"),
  assessmentResolutionMessage: document.querySelector("#assessment-resolution-message"),
  saveAssessmentResolutionButton: document.querySelector("#save-assessment-resolution"),
  closeAssessmentResolutionDialog: document.querySelector("#close-assessment-resolution-dialog"),
  cancelAssessmentResolutionButton: document.querySelector("#cancel-assessment-resolution"),
  correctAssessmentFactsButton: document.querySelector("#correct-assessment-facts"),
  jobFactGrid: document.querySelector("#job-fact-grid"),
  jobContextGrid: document.querySelector("#job-context-grid"),
  jobCompensationList: document.querySelector("#job-compensation-list"),
  jobAssessment: document.querySelector("#job-assessment"),
  jobGateList: document.querySelector("#job-gate-list"),
  jobEligibilitySection: document.querySelector("#job-eligibility-section"),
  editJobGatesButton: document.querySelector("#edit-job-gates-button"),
  cancelJobGatesButton: document.querySelector("#cancel-job-gates-button"),
  saveJobGatesButton: document.querySelector("#save-job-gates-button"),
  jobGatesMessage: document.querySelector("#job-gates-message"),
  addJobSkillButton: document.querySelector("#job-skill-button"),
  jobTechnologyList: document.querySelector("#job-technology-list"),
  jobRequirementsSection: document.querySelector("#job-requirements-section"),
  editJobRequirementsButton: document.querySelector("#edit-job-requirements-button"),
  cancelJobRequirementsButton: document.querySelector("#cancel-job-requirements-button"),
  saveJobRequirementsButton: document.querySelector("#save-job-requirements-button"),
  jobRequirementsMessage: document.querySelector("#job-requirements-message"),
  jobRequirementList: document.querySelector("#job-requirement-list"),
  jobResponsibilityList: document.querySelector("#job-responsibility-list"),
  jobReviewNotice: document.querySelector("#job-review-notice"),
  jobCoverageSection: document.querySelector("#job-coverage-section"),
  jobCoverageSummary: document.querySelector("#job-coverage-summary"),
  jobCoverageMetrics: document.querySelector("#job-coverage-metrics"),
  jobReviewLineList: document.querySelector("#job-review-line-list"),
  skillQuickJobLevelField: document.querySelector("#skill-quick-job-level-field"),
  skillQuickJobLevel: document.querySelector("#skill-quick-job-level"),
  extractionDetails: document.querySelector("#extraction-details"),
  extractionPath: document.querySelector("#extraction-path"),
  metricStrip: document.querySelector("#metric-strip"),
  technologyList: document.querySelector("#technology-list"),
  sectionList: document.querySelector("#section-list"),
  documentBlockList: document.querySelector("#document-block-list"),
  documentBlockCount: document.querySelector("#document-block-count"),
  bulletList: document.querySelector("#bullet-list"),
  bulletCount: document.querySelector("#bullet-count"),
  reviewBackButton: document.querySelector("#review-back-button"),
  reviewPath: document.querySelector("#review-path"),
  reviewStatus: document.querySelector("#review-status"),
  reviewCounts: document.querySelector("#review-counts"),
  reviewMessage: document.querySelector("#review-message"),
  keepApprovedResume: document.querySelector("#keep-approved-resume"),
  keepApprovedResumeButton: document.querySelector("#keep-approved-resume-button"),
  keepApprovedResumeMessage: document.querySelector("#keep-approved-resume-message"),
  removeApprovedImport: document.querySelector("#remove-approved-import"),
  removeApprovedImportButton: document.querySelector("#remove-approved-import-button"),
  removeApprovedImportMessage: document.querySelector("#remove-approved-import-message"),
  completionGate: document.querySelector("#completion-gate"),
  completionStatus: document.querySelector("#completion-status"),
  completionCounts: document.querySelector("#completion-counts"),
  completionBlockList: document.querySelector("#completion-block-list"),
  approvalPreview: document.querySelector("#approval-preview"),
  approvalEngine: document.querySelector("#approval-engine"),
  approvalPreviewSummary: document.querySelector("#approval-preview-summary"),
  approvalPreviewList: document.querySelector("#approval-preview-list"),
  closeApprovalButton: document.querySelector("#close-approval-button"),
  saveApprovalDecisionsButton: document.querySelector("#save-approval-decisions-button"),
  applyApprovalButton: document.querySelector("#apply-approval-button"),
  buildReviewButton: document.querySelector("#build-review-button"),
  suggestCardsButton: document.querySelector("#suggest-cards-button"),
  acceptSuggestionsButton: document.querySelector("#accept-suggestions-button"),
  saveDraftButton: document.querySelector("#save-draft-button"),
  prepareApprovalButton: document.querySelector("#prepare-approval-button"),
  deleteCardButton: document.querySelector("#delete-card-button"),
  restoreCardButton: document.querySelector("#restore-card-button"),
  ungroupCardButton: document.querySelector("#ungroup-card-button"),
  acceptCardButton: document.querySelector("#accept-card-button"),
  entityHierarchy: document.querySelector("#entity-hierarchy"),
  reviewWorkspace: document.querySelector("#review-workspace"),
  reviewInspector: document.querySelector("#review-inspector"),
  closeReviewInspector: document.querySelector("#close-review-inspector"),
  reviewInspectorBackdrop: document.querySelector("#review-inspector-backdrop"),
  candidateCount: document.querySelector("#candidate-count"),
  candidateCard: document.querySelector("#candidate-card"),
  candidateSummary: document.querySelector("#candidate-summary"),
  experienceCardCount: document.querySelector("#experience-card-count"),
  experienceCardRow: document.querySelector("#experience-card-row"),
  educationCardCount: document.querySelector("#education-card-count"),
  educationCardRow: document.querySelector("#education-card-row"),
  certificationCardCount: document.querySelector("#certification-card-count"),
  certificationCardRow: document.querySelector("#certification-card-row"),
  projectCardCount: document.querySelector("#project-card-count"),
  projectCardRow: document.querySelector("#project-card-row"),
  skillCardCount: document.querySelector("#skill-card-count"),
  skillCardRow: document.querySelector("#skill-card-row"),
  cardEditor: document.querySelector("#card-editor"),
  reviewBlockList: document.querySelector("#review-block-list"),
  runSelect: document.querySelector("#run-select"),
  runSummary: document.querySelector("#run-summary"),
  diagnosticEvents: document.querySelector("#diagnostic-events"),
  diagnosticsHistory: document.querySelector("#diagnostics-history"),
  diagnosticsHistoryStatus: document.querySelector("#diagnostics-history-status"),
  diagnosticsHistoryRetry: document.querySelector("#diagnostics-history-retry"),
  statusDot: document.querySelector("#status-dot"),
  logStage: document.querySelector("#log-stage"),
  logStatus: document.querySelector("#log-status"),
  logDuration: document.querySelector("#log-duration"),
  logRun: document.querySelector("#log-run"),
  appVersion: document.querySelector("#app-version"),
  buttonTooltip: document.querySelector("#button-tooltip"),
  toastRegion: document.querySelector("#toast-region"),
};

const toast = createToastManager(elements.toastRegion);
const materialReviews = {
  resume: new MaterialReview({ root: document.querySelector("#resume-material-fact-review"), api, kind: "resume", onChange: updateMaterialReviewControls }),
  cover_letter: new MaterialReview({ root: document.querySelector("#cover-letter-fact-review"), api, kind: "cover_letter", onChange: updateMaterialReviewControls }),
};

function syncMaterialReviews() {
  const jobId = state.activeView === "application-materials" ? state.materialEditorJobId : null;
  materialReviews.resume.setContext({ jobId, editor: state.resumeMaterialEditor, dirty: state.resumeMaterialDirty, busy: state.resumeMaterialLoading });
  materialReviews.cover_letter.setContext({ jobId, editor: state.materialEditor, dirty: state.materialEditorDirty, busy: state.materialEditorLoading });
}

function updateMaterialReviewControls() {
  for (const kind of ["resume", "cover_letter"]) {
    const resume = kind === "resume";
    const editor = resume ? state.resumeMaterialEditor : state.materialEditor;
    const dirty = resume ? state.resumeMaterialDirty : state.materialEditorDirty;
    const loading = resume ? state.resumeMaterialLoading : state.materialEditorLoading;
    const review = materialReviews[kind];
    const buttons = resume ? elements.resumeMaterialExportButtons : elements.coverLetterExportButtons;
    const draftBlocked = !editor || loading || (resume && (!editor.persisted || dirty || !editor.validation?.ready_to_export));
    buttons.forEach((button) => { button.disabled = draftBlocked || review.busy || (editor?.persisted && !dirty && review.blocksExport); });
    const agentButton = resume ? elements.copyResumeAiCommand : elements.copyCoverLetterAiCommand;
    agentButton.disabled = !editor || loading || review.busy || review.supported || Boolean(review.error);
    agentButton.dataset.tooltip = review.supported
      ? "The agent adapter is not yet available in this Go migration preview. Edit this draft here."
      : "Copy the bounded Career Hub agent command. No external AI call is made by the browser.";
  }
}
const experienceDateRange = createMonthRangePicker(elements.vaultDateRange, { label: "Employment dates", required: true });
const projectDateRange = createMonthRangePicker(elements.projectDateRange, { label: "Project dates" });
const educationDateRange = createMonthRangePicker(elements.educationDateRange, { label: "Attendance dates" });
const graduationMonth = createMonthPicker(elements.educationGraduationMonth, { label: "Graduation month" });
const certificationCompletedMonth = createMonthPicker(elements.certificationCompletedMonth, { label: "Completion month", required: true });

const jobDraftCombobox = createSearchableCombobox(elements.jobDraftCombobox, {
  inputId: "job-draft-select-input",
  labelledBy: "job-draft-select-label",
  placeholder: "Search saved jobs",
  onChange: (jobId) => {
    void selectJob(jobId);
  },
});
const applicationResumeCombobox = createSearchableCombobox(elements.applicationResumeCombobox, {
  inputId: "application-resume-select-input",
  labelledBy: "application-resume-select-label",
  placeholder: "Search by resume name",
  emptyMessage: "No saved resumes match.",
  onChange: (resumeId) => {
    void selectApplicationResume(resumeId);
  },
});
renderNavigation(elements.primaryNavigation, state.activeView);

let activeTooltipTarget = null;
let tooltipTimer = null;

function cancelScheduledTooltip() {
  if (tooltipTimer !== null) window.clearTimeout(tooltipTimer);
  tooltipTimer = null;
}

function hideButtonTooltip() {
  cancelScheduledTooltip();
  if (!activeTooltipTarget) return;
  const describedBy = (activeTooltipTarget.getAttribute("aria-describedby") || "")
    .split(/\s+/)
    .filter((id) => id && id !== "button-tooltip");
  if (describedBy.length) {
    activeTooltipTarget.setAttribute("aria-describedby", describedBy.join(" "));
  } else {
    activeTooltipTarget.removeAttribute("aria-describedby");
  }
  activeTooltipTarget = null;
  elements.buttonTooltip.hidden = true;
}

function positionButtonTooltip(button) {
  const buttonRect = button.getBoundingClientRect();
  const tooltipRect = elements.buttonTooltip.getBoundingClientRect();
  const margin = 8;
  const left = Math.min(
    window.innerWidth - tooltipRect.width - margin,
    Math.max(margin, buttonRect.left + (buttonRect.width - tooltipRect.width) / 2)
  );
  let top = buttonRect.top - tooltipRect.height - margin;
  if (top < margin) top = buttonRect.bottom + margin;
  elements.buttonTooltip.style.left = `${Math.round(left)}px`;
  elements.buttonTooltip.style.top = `${Math.round(top)}px`;
}

function showButtonTooltip(button) {
  const message = button?.dataset.tooltip?.trim();
  if (!message) return;
  hideButtonTooltip();
  activeTooltipTarget = button;
  elements.buttonTooltip.textContent = message;
  elements.buttonTooltip.hidden = false;
  const describedBy = new Set(
    (button.getAttribute("aria-describedby") || "").split(/\s+/).filter(Boolean)
  );
  describedBy.add("button-tooltip");
  button.setAttribute("aria-describedby", [...describedBy].join(" "));
  positionButtonTooltip(button);
}

function initializeButtonTooltips() {
  document.addEventListener("pointerover", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (!target) return;
    cancelScheduledTooltip();
    tooltipTimer = window.setTimeout(() => showButtonTooltip(target), 350);
  });
  document.addEventListener("pointerout", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (target && !target.contains(event.relatedTarget)) hideButtonTooltip();
  });
  document.addEventListener("focusin", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (target && target.dataset.tooltipFocus !== "false") showButtonTooltip(target);
  });
  document.addEventListener("focusout", (event) => {
    if (event.target === activeTooltipTarget) hideButtonTooltip();
  });
  document.addEventListener("click", hideButtonTooltip);
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      hideButtonTooltip();
      if (state.applicationDrawerOpen) closeApplicationDrawer();
      if (state.demandGapsDrawerOpen) closeDemandGapDrawer();
    }
  });
  const syncFocusedTooltip = () => {
    if (activeTooltipTarget && document.activeElement === activeTooltipTarget) {
      positionButtonTooltip(activeTooltipTarget);
    } else {
      hideButtonTooltip();
    }
  };
  window.addEventListener("resize", syncFocusedTooltip);
  window.addEventListener("scroll", syncFocusedTooltip, true);
}

function syncJobWorkflowView() {
  const workflow = isJobWorkflowView(state.activeView) ? state.activeView : "job-intake";
  const copy = jobWorkflowCopy(workflow);
  elements.jobWorkflowTitle.textContent = copy.title;
  elements.jobWorkflowDescription.textContent = copy.description;
  elements.jobWorkflowState.textContent = copy.state;
  elements.jobWorkflowState.classList.toggle("hidden", workflow === "demand-gaps");
  elements.demandGapsHeaderControls.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.jobFeedWorkflow.classList.toggle("workflow-hidden", workflow !== "job-feed");
  elements.jobIntakeWorkflow.classList.toggle("workflow-hidden", workflow !== "job-intake");
  elements.jobLibraryWorkflow.classList.toggle("workflow-hidden", workflow !== "job-library");
  elements.jobDigestSection.classList.toggle("workflow-hidden", workflow !== "job-library");
  elements.applicationLedgerWorkflow.classList.toggle("workflow-hidden", workflow !== "applications");
  elements.demandGapsWorkflow.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.demandGapDrawer.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.demandGapDrawerBackdrop.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.jobApplicationSection.classList.toggle("workflow-hidden", workflow !== "applications");
  elements.applicationDrawerBackdrop.classList.toggle("workflow-hidden", workflow !== "applications");
  if (workflow !== "job-library") hideJobEditForm();
  if (workflow !== "applications") state.applicationDrawerOpen = false;
  if (workflow !== "demand-gaps") closeDemandGapDrawer({ restoreFocus: false });
  if (workflow === "applications") {
    renderApplicationLedger();
    renderApplicationEmailUpdates();
  }
  if (workflow === "job-library") setJobWorkspaceTab(state.jobWorkspaceTab);
  renderJobApplication();
}

function setJobWorkspaceTab(tab, { focus = false } = {}) {
  const nextTab = ["prepare", "assessment", "details"].includes(tab) ? tab : "prepare";
  state.jobWorkspaceTab = nextTab;
  elements.jobWorkspaceTabButtons.forEach((button) => {
    const active = button.dataset.jobWorkspaceTab === nextTab;
    button.setAttribute("aria-selected", String(active));
    button.tabIndex = active ? 0 : -1;
    button.classList.toggle("is-active", active);
    if (active && focus) button.focus();
  });
  const groups = {
    prepare: [elements.jobWorkspaceMaterials, elements.jobInstructionChecklist, elements.jobContactCandidates],
    assessment: [elements.jobAssessment, elements.jobDigestColumns],
    details: [
      elements.jobOverviewLayout,
      elements.jobEditMessage,
      elements.jobDetailGrid,
      elements.jobSourcePreview,
      elements.jobCoverageSection,
    ],
  };
  Object.entries(groups).forEach(([group, nodes]) => {
    nodes.filter(Boolean).forEach((node) => {
      const hidden = group !== nextTab;
      node.hidden = hidden;
      node.classList.toggle("job-workspace-panel-hidden", hidden);
    });
  });
}

function setResumeIntent(intent) {
  const showVaultIntake = intent === "vault";
  elements.resumeVaultIntake.hidden = !showVaultIntake;
  elements.resumeApplicationIntent.hidden = showVaultIntake;
  if (showVaultIntake && state.activeView === "intake") {
    void loadResumeCollections().catch((error) => {
      elements.runMessage.className = "form-message error";
      elements.runMessage.textContent = formatRequestError(error);
    });
  }
}

function selectResumeIntent(input) {
  input.checked = true;
  setResumeIntent(input.value);
}

function openApplicationResumeIntake() {
  const applicationIntent = elements.resumeIntentInputs.find(
    (input) => input.value === "applications"
  );
  if (!applicationIntent) return;
  selectResumeIntent(applicationIntent);
  switchView("intake");
  applicationIntent.focus();
}

function localRouteForView(viewName) {
  if (viewName === "review" && state.reviewSourceId) {
    return `#review/${encodeURIComponent(state.reviewSourceId)}`;
  }
  if (viewName === "application-materials" && state.materialEditorJobId) {
    return `#application-materials/${encodeURIComponent(state.materialEditorJobId)}`;
  }
  if (viewName === "job-feed" && state.jobFeedDiscoveryId) {
    const intent = state.jobFeedIntent === "prepare" ? "/prepare" : "";
    return `#job-feed/${encodeURIComponent(state.jobFeedDiscoveryId)}${intent}`;
  }
  if (viewName === "skills" && state.skillsHubMode === "all") return "#skills/all";
  return `#${viewName}`;
}

function replaceLocalRouteForCurrentView() {
  const route = localRouteForView(state.activeView);
  if (window.location.hash !== route) {
    window.history.replaceState({ careerHubView: state.activeView }, "", route);
  }
}

function discardLocalReviewChanges() {
  state.reviewDirty = false;
  state.approvalPreview = null;
}

function beginReviewLoad(sourceId) {
  reviewLoadEpoch += 1;
  state.reviewSourceId = sourceId;
  state.reviewDetail = null;
  state.approvalPreview = null;
  state.reviewDirty = false;
  state.selectedEntityId = null;
  state.reviewInspectorOpen = false;
  elements.keepApprovedResumeMessage.textContent = "";
  elements.removeApprovedImportMessage.className = "form-message";
  elements.removeApprovedImportMessage.textContent = "This is permanent. Keep the file for applications first if you may need its exact original bytes later.";
  clearExtractionDetails();
  return reviewLoadEpoch;
}

function isCurrentReviewLoad(sourceId, epoch) {
  return (
    state.activeView === "review"
    && state.reviewSourceId === sourceId
    && reviewLoadEpoch === epoch
  );
}

function activeReviewLoadRequest() {
  const sourceId = state.reviewSourceId;
  if (!sourceId || state.activeView !== "review") return null;
  return { sourceId, epoch: reviewLoadEpoch };
}

function currentReviewRequest() {
  const request = activeReviewLoadRequest();
  if (!request || state.reviewDetail?.source_id !== request.sourceId) return null;
  return request;
}

function isCurrentReviewRequest(request) {
  return Boolean(request) && isCurrentReviewLoad(request.sourceId, request.epoch)
    && state.reviewDetail?.source_id === request.sourceId;
}

function currentApprovedReviewSourceId() {
  const request = currentReviewRequest();
  if (state.reviewDetail?.approval_application?.status !== "applied") return null;
  return request?.sourceId || null;
}

async function confirmReviewNavigation(viewName, sourceId = null) {
  const remainsOnMaterialEditor = (
    state.activeView === "application-materials"
    && viewName === "application-materials"
  );
  if ((state.materialEditorDirty || state.resumeMaterialDirty || state.resumePlanDirty)
      && !remainsOnMaterialEditor) {
    if (!window.confirm("Discard unsaved application-material changes?")) return false;
    state.materialEditorDirty = false;
    state.resumeMaterialDirty = false;
    state.resumePlanDirty = false;
  }
  const remainsOnCurrentReview = (
    state.activeView === "review"
    && viewName === "review"
    && sourceId === state.reviewSourceId
  );
  if (!state.reviewDirty || remainsOnCurrentReview) return true;
  if (!window.confirm("Discard unsaved review changes? Save the draft first if you want to keep these decisions.")) {
    return false;
  }
  discardLocalReviewChanges();
  return true;
}

async function navigateToView(viewName) {
  if (!(await confirmReviewNavigation(viewName))) return false;
  switchView(viewName);
  return true;
}

async function navigateFromHelp(destination) {
  if (destination === "job-feed-email") {
    if (!(await navigateToView("job-feed"))) return;
    document.querySelector('[data-discovery-mode="email"]')?.click();
    return;
  }
  if (["diagnostics", "profile"].includes(destination)) {
    await navigateToView(destination);
  }
}

function setMobileNavigation(open) {
  const nextOpen = mobileNavigationMedia.matches && Boolean(open);
  document.body.classList.toggle("navigation-open", nextOpen);
  elements.mobileNavigationToggle.setAttribute("aria-expanded", String(nextOpen));
  elements.sidebarBackdrop.hidden = !nextOpen;
  elements.sidebar.inert = mobileNavigationMedia.matches && !nextOpen;
  if (mobileNavigationMedia.matches) {
    elements.sidebar.setAttribute("aria-hidden", String(!nextOpen));
  } else {
    elements.sidebar.removeAttribute("aria-hidden");
  }
  if (nextOpen) window.requestAnimationFrame(() => elements.mobileNavigationClose.focus());
}

function openMobileQuickActions() {
  const addMenu = elements.primaryNavigation.querySelector(".nav-add-menu");
  if (!addMenu) return;
  addMenu.open = true;
  setMobileNavigation(true);
  window.requestAnimationFrame(() => addMenu.querySelector("summary")?.focus());
}

function scheduleViewScroll(route, restoreScroll) {
  window.requestAnimationFrame(() => {
    const top = restoreScroll ? Number(viewScrollPositions.get(route) || 0) : 0;
    window.scrollTo({ top, left: 0, behavior: "auto" });
  });
}

function viewFromLocalRoute() {
  const [viewName, encodedSourceId, encodedIntent] = window.location.hash.slice(1).split("/");
  let sourceId = null;
  try {
    sourceId = encodedSourceId ? decodeURIComponent(encodedSourceId) : null;
  } catch {
    sourceId = null;
  }
  if (viewName === "review" && sourceId?.startsWith("src_") && !/[\\/]/.test(sourceId)) {
    return { viewName, sourceId };
  }
  if (viewName === "application-materials" && sourceId && !/[\\/]/.test(sourceId)) {
    return { viewName, sourceId: null, materialJobId: sourceId };
  }
  if (
    viewName === "job-feed"
    && sourceId?.startsWith("discovery_")
    && !/[\\/]/.test(sourceId)
  ) {
    return {
      viewName,
      sourceId: null,
      discoveryId: sourceId,
      discoveryIntent: encodedIntent === "prepare" ? "prepare" : null,
    };
  }
  if (viewName === "competencies") {
    return { viewName: "skills", sourceId: null, skillsMode: "all", legacyRoute: true };
  }
  if (viewName === "skills") {
    return { viewName, sourceId: null, skillsMode: encodedSourceId === "all" ? "all" : "groups" };
  }
  if (viewName === "profile" && encodedSourceId === "application-helper") {
    return { viewName, sourceId: null, applicationHelperIntent: "configure" };
  }
  if (
    viewName === "resume-library"
    && /^resume_[a-f0-9]{32}$/.test(sourceId || "")
  ) {
    return { viewName, sourceId: null, resumeLibraryFocusId: sourceId };
  }
  const supportedViews = new Set([
    "vault", "profile", "projects", "skills", "education", "job-intake", "job-feed",
    "job-library", "applications", "application-materials", "contacts", "demand-gaps", "resume-library", "intake", "documents", "diagnostics",
  ]);
  return { viewName: supportedViews.has(viewName) ? viewName : "vault", sourceId: null };
}

function switchView(viewName, { writeHistory = true, restoreScroll = false } = {}) {
  const priorRoute = localRouteForView(state.activeView);
  if (state.activeView !== viewName) viewScrollPositions.set(priorRoute, window.scrollY);
  if (viewName === "competencies") {
    viewName = "skills";
    setSkillsHubMode("all", { writeHistory: false });
  }
  if (viewName !== "review") state.reviewInspectorOpen = false;
  state.activeView = viewName;
  if (writeHistory) {
    const route = localRouteForView(viewName);
    if (window.location.hash !== route) {
      window.history.pushState({ careerHubView: viewName }, "", route);
    }
  }
  const route = localRouteForView(viewName);
  renderNavigation(elements.primaryNavigation, viewName);
  setMobileNavigation(false);
  const physicalView = isJobWorkflowView(viewName) ? "jobs" : viewName;
  document.querySelectorAll(".view").forEach((view) => {
    view.classList.toggle("active", view.id === `view-${physicalView}`);
  });
  syncReviewInspector();
  if (physicalView === "jobs") syncJobWorkflowView();
  if (viewName === "diagnostics") {
    renderDiagnostics();
    startDiagnosticsPolling();
  } else {
    stopDiagnosticsPolling();
  }
  void ensureViewData(viewName).catch((error) => {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  });
  scheduleViewScroll(route, restoreScroll);
}

async function restoreLocalRoute() {
  const {
    viewName,
    sourceId,
    skillsMode,
    materialJobId,
    discoveryId,
    discoveryIntent,
    applicationHelperIntent,
    resumeLibraryFocusId,
    legacyRoute = false,
  } = viewFromLocalRoute();
  if (!(await confirmReviewNavigation(viewName, sourceId))) {
    replaceLocalRouteForCurrentView();
    return;
  }
  if (viewName === "review" && sourceId === state.reviewSourceId && state.reviewDirty) {
    switchView(viewName, { writeHistory: false, restoreScroll: true });
    return;
  }
  if (viewName === "skills" && skillsMode) {
    setSkillsHubMode(skillsMode, { persist: true, writeHistory: false });
  }
  if (viewName === "application-materials" && materialJobId) {
    state.materialEditorJobId = materialJobId;
    state.selectedJobId = materialJobId;
  }
  if (viewName === "job-feed") {
    state.jobFeedDiscoveryId = discoveryId || null;
    state.jobFeedIntent = discoveryIntent || null;
    setDiscoveryRouteTarget(discoveryId || null, discoveryIntent || null);
  }
  if (viewName === "resume-library") {
    state.resumeLibraryFocusId = resumeLibraryFocusId || null;
  }
  const reviewEpoch = viewName === "review" && sourceId ? beginReviewLoad(sourceId) : null;
  switchView(viewName, { writeHistory: false, restoreScroll: true });
  if (legacyRoute) replaceLocalRouteForCurrentView();
  if (resumeLibraryFocusId) replaceLocalRouteForCurrentView();
  if (applicationHelperIntent === "configure") {
    replaceLocalRouteForCurrentView();
    await openApplicationHelperConfiguration();
  }
  if (viewName === "review" && sourceId) await loadReview(sourceId, { epoch: reviewEpoch });
  if (viewName === "application-materials" && materialJobId) {
    await loadCoverLetterMaterial();
  }
}

const requestJson = api.request.bind(api);

async function getVaultSnapshot({ refresh = false } = {}) {
  if (refresh) {
    vaultSnapshot = null;
    vaultSnapshotPromise = null;
  }
  if (vaultSnapshot) return vaultSnapshot;
  if (!vaultSnapshotPromise) {
    vaultSnapshotPromise = api.getVaultSnapshot()
      .then((payload) => {
        vaultSnapshot = payload;
        return payload;
      })
      .catch((error) => {
        vaultSnapshot = null;
        throw error;
      })
      .finally(() => {
        vaultSnapshotPromise = null;
      });
  }
  return vaultSnapshotPromise;
}

function vaultLines(value, splitCommas = false) {
  const pattern = splitCommas ? /[\n,]+/ : /\r?\n/;
  return [...new Set(
    String(value || "").split(pattern).map((item) => item.trim()).filter(Boolean)
  )];
}

function applyCareerVaultLayoutState() {
  const layout = state.careerVaultLayout === "cards" ? "cards" : "list";
  elements.careerVaultWorkspaces.forEach((workspace) => {
    const surface = workspace.dataset.vaultSurface;
    workspace.dataset.vaultLayout = layout;
    workspace.classList.toggle(
      "is-card-detail-open",
      layout === "cards" && state.vaultCardDetailOpen[surface] === true,
    );
  });
  elements.careerVaultLayoutButtons.forEach((button) => {
    const selected = button.dataset.vaultLayout === layout;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-pressed", String(selected));
  });
}

function loadCareerVaultLayoutPreference() {
  let storedLayout = "";
  try {
    storedLayout = window.localStorage.getItem(CAREER_VAULT_LAYOUT_KEY) || "";
  } catch (_error) {
    storedLayout = "";
  }
  state.careerVaultLayout = ["list", "cards"].includes(storedLayout) ? storedLayout : "list";
  Object.keys(state.vaultCardDetailOpen).forEach((surface) => {
    state.vaultCardDetailOpen[surface] = false;
  });
  applyCareerVaultLayoutState();
}

function setCareerVaultLayout(layout, { persist = true } = {}) {
  if (!["list", "cards"].includes(layout)) return;
  state.careerVaultLayout = layout;
  Object.keys(state.vaultCardDetailOpen).forEach((surface) => {
    state.vaultCardDetailOpen[surface] = false;
  });
  if (persist) {
    try {
      window.localStorage.setItem(CAREER_VAULT_LAYOUT_KEY, layout);
    } catch (_error) {
      // The visual preference remains usable when browser storage is unavailable.
    }
  }
  applyCareerVaultLayoutState();
}

function openCareerVaultCardDetail(surface) {
  if (state.careerVaultLayout !== "cards" || !(surface in state.vaultCardDetailOpen)) return;
  Object.keys(state.vaultCardDetailOpen).forEach((name) => {
    state.vaultCardDetailOpen[name] = name === surface;
  });
  applyCareerVaultLayoutState();
}

function closeCareerVaultCardDetail(surface) {
  if (!(surface in state.vaultCardDetailOpen)) return;
  state.vaultCardDetailOpen[surface] = false;
  applyCareerVaultLayoutState();
}

function renderVaultCardPreview(items, fallback) {
  const values = (items || []).map((item) => String(item || "").trim()).filter(Boolean).slice(0, 4);
  const rows = values.length
    ? values.map((item) => `<span>${escapeHtml(item)}</span>`).join("")
    : `<span class="is-empty">${escapeHtml(fallback)}</span>`;
  return `<span class="vault-card-preview vault-card-only">${rows}</span>`;
}

function renderVaultCardOpenLabel() {
  return `<span class="vault-card-open vault-card-only"><span>Open record</span>${registryChevronIcon()}</span>`;
}

function selectedVaultExperience() {
  return state.vaultExperiences.find(
    (record) => record.id === state.selectedExperienceId
  ) || null;
}

function renderVaultCategoryOptions() {
  const selectedFilter = elements.vaultCategoryFilter.value;
  elements.vaultCategoryFilter.innerHTML = '<option value="">All categories</option>'
    + state.vaultCategories.map(
      (category) => `<option value="${escapeHtml(category)}" translate="no">${escapeHtml(category)}</option>`
    ).join("");
  if (state.vaultCategories.includes(selectedFilter)) {
    elements.vaultCategoryFilter.value = selectedFilter;
  }
  elements.vaultCategoryOptions.innerHTML = state.vaultCategories.map(
    (category) => `<option value="${escapeHtml(category)}"></option>`
  ).join("");
}

function statusVaultExperiences() {
  const archived = state.vaultVisibility === "archived";
  return state.vaultExperiences.filter((record) => (record.archived === true) === archived);
}

function filteredVaultExperiences() {
  const query = elements.vaultSearch.value.trim().toLocaleLowerCase();
  const category = elements.vaultCategoryFilter.value;
  return statusVaultExperiences().filter((record) => {
    if (category && record.category !== category) return false;
    if (!query) return true;
    const searchText = [
      record.company,
      record.role,
      record.category,
      ...(record.technologies || []),
      ...(record.proof_points || []),
    ].join(" ").toLocaleLowerCase();
    return searchText.includes(query);
  });
}

function renderVaultExperienceList() {
  const records = filteredVaultExperiences();
  const statusRecords = statusVaultExperiences();
  const activeCount = state.vaultExperiences.filter((record) => record.archived !== true).length;
  const archivedCount = state.vaultExperiences.length - activeCount;
  elements.vaultArchivedCount.textContent = archivedCount;
  updateArchiveScopeToggle(
    elements.vaultArchivedFilter,
    state.vaultVisibility === "archived",
    "experience",
  );
  elements.vaultExperienceCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.vaultExperienceList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedExperienceId;
      return `
        <button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-vault-experience-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open ${escapeHtml(record.company)} experience for editing.">
          <span class="vault-card-kind vault-card-only">Experience</span>
          <span class="vault-record-main">
            <strong>${escapeHtml(record.company)}</strong>
            <span>${escapeHtml(record.role)}</span>
          </span>
          <span class="vault-record-meta">
            <span>${escapeHtml(record.dates)}</span>
            <span>${escapeHtml(record.category)}</span>
          </span>
          ${renderVaultCardPreview(record.technologies, "No technologies listed")}
          <span class="vault-record-counts">${(record.technologies || []).length} technologies / ${(record.proof_points || []).length} items${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : '<div class="empty-state">No experience records match these filters.</div>';
}

function manualSkillNodes(kind) {
  return kind === "experience"
    ? { pills: elements.vaultSkillPills, input: elements.vaultSkillInput, options: elements.vaultSkillOptions, pending: elements.vaultPendingSkills, count: elements.vaultSkillCount }
    : { pills: elements.projectSkillPills, input: elements.projectSkillInput, options: elements.projectSkillOptions, pending: elements.projectPendingSkills, count: elements.projectSkillCount };
}

function manualSkillNameIndex() {
  const index = new Map();
  registrySkillIndex().forEach((skill) => {
    [skill.name, ...(skill.aliases || [])].forEach((name) => index.set(String(name).trim().toLocaleLowerCase(), skill));
  });
  return index;
}

function setManualSkillsFromRecord(kind, record) {
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  const names = manualSkillNameIndex();
  selection.ids = [...new Set((record?.competency_ids || []).filter((id) => skills.has(id)))];
  selection.pending = [];
  (record?.technologies || []).forEach((name) => {
    const matched = names.get(String(name).trim().toLocaleLowerCase());
    if (matched && !selection.ids.includes(matched.id)) selection.ids.push(matched.id);
    if (!matched) selection.pending.push({ name, primary_group_id: state.skillsRegistry?.default_custom_group_id || null });
  });
  renderManualSkillSelector(kind);
}

function renderManualSkillSelector(kind) {
  const nodes = manualSkillNodes(kind);
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  const groups = (state.skillsRegistry?.groups || []).filter((group) => !group.archived);
  nodes.count.textContent = `${selection.ids.length + selection.pending.length} selected`;
  nodes.options.innerHTML = [...new Set([...skills.values()].filter((skill) => !skill.archived).flatMap((skill) => [skill.name, ...(skill.aliases || [])]))].sort().map((name) => `<option value="${escapeHtml(name)}"></option>`).join("");
  nodes.pills.innerHTML = selection.ids.length ? selection.ids.map((id) => {
    const skill = skills.get(id);
    const archived = skill?.archived === true;
    const title = archived
      ? `${skill?.group?.name || "Skills Registry"} · archived historical link`
      : skill?.group?.name || "Skills Registry";
    return `<span class="review-skill-pill registry-skill-${escapeHtml(skill?.origin || "core")}${archived ? " is-archived" : ""}" title="${escapeHtml(title)}"><span>${escapeHtml(skill?.name || id)}${archived ? '<small>Archived</small>' : ""}</span><button type="button" data-manual-skill-remove="${escapeHtml(id)}" aria-label="Remove ${escapeHtml(skill?.name || id)}">&times;</button></span>`;
  }).join("") : (selection.pending.length ? "" : '<span class="empty-state compact">No skills selected.</span>');
  nodes.pending.innerHTML = selection.pending.length ? `<p class="section-kicker">New custom skills on save</p>${selection.pending.map((item, index) => `<div class="review-pending-skill"><span class="review-skill-pill registry-skill-custom"><span>${escapeHtml(item.name)}</span><button type="button" data-manual-pending-remove="${index}" aria-label="Remove ${escapeHtml(item.name)}">&times;</button></span><label><span>Group</span><select data-manual-pending-group="${index}">${groups.map((group) => `<option value="${escapeHtml(group.id)}" ${group.id === item.primary_group_id ? "selected" : ""} translate="no">${escapeHtml(group.name)} · ${escapeHtml(group.origin)}</option>`).join("")}</select></label></div>`).join("")}` : "";
}

function addManualSkills(kind) {
  const nodes = manualSkillNodes(kind);
  const selection = state.manualSkills[kind];
  const names = manualSkillNameIndex();
  String(nodes.input.value || "").split(/[,;\n]+/).map((value) => value.trim().replace(/\s+/g, " ")).filter(Boolean).forEach((name) => {
    const matched = names.get(name.toLocaleLowerCase());
    if (matched) {
      if (!selection.ids.includes(matched.id)) selection.ids.push(matched.id);
      selection.pending = selection.pending.filter((item) => item.name.toLocaleLowerCase() !== name.toLocaleLowerCase());
    } else if (!selection.pending.some((item) => item.name.toLocaleLowerCase() === name.toLocaleLowerCase())) {
      selection.pending.push({ name, primary_group_id: state.skillsRegistry?.default_custom_group_id || null });
    }
  });
  nodes.input.value = "";
  if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true;
  renderManualSkillSelector(kind);
}

function manualSkillRecordFields(kind) {
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  return {
    competency_ids: [...selection.ids],
    pending_skills: selection.pending.map((item) => ({ ...item })),
    registry_revision: state.skillsRegistry?.revision || "",
    technologies: selection.ids.map((id) => skills.get(id)?.name).filter(Boolean),
  };
}

function setArchiveActionButton(button, restoring, noun) {
  const label = `${restoring ? "Restore" : "Archive"} ${noun}`;
  button.classList.add("icon-action-button");
  button.setAttribute("aria-label", label);
  button.innerHTML = restoring
    ? '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 7 4-3 4 3M12 8v7"></path></svg><span class="visually-hidden">Restore</span>'
    : '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg><span class="visually-hidden">Archive</span>';
}

function currentVaultProofPoints() {
  return normalizedEvidenceItems(state.vaultProofPoints);
}

function renderVaultProofPointEditor() {
  const points = state.vaultProofPoints;
  const count = currentVaultProofPoints().length;
  elements.vaultProofPointCount.textContent = `${count} item${count === 1 ? "" : "s"}`;
  elements.vaultProofPointList.innerHTML = points.length
    ? points.map((point, index) => `
      <article class="vault-proof-point-card" data-vault-proof-point-card="${index}">
        <div class="vault-proof-point-card-heading">
          <span class="vault-proof-point-number" aria-hidden="true">${index + 1}</span>
          <label for="vault-proof-point-${index}">Evidence item ${index + 1}</label>
          <div class="vault-proof-point-actions" aria-label="Evidence item ${index + 1} actions">
            <button type="button" class="icon-action-button" data-vault-proof-move="up" data-vault-proof-index="${index}" aria-label="Move evidence item ${index + 1} up" ${index === 0 ? "disabled" : ""} title="Move up">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 14 6-6 6 6"></path></svg>
            </button>
            <button type="button" class="icon-action-button" data-vault-proof-move="down" data-vault-proof-index="${index}" aria-label="Move evidence item ${index + 1} down" ${index === points.length - 1 ? "disabled" : ""} title="Move down">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 10 6 6 6-6"></path></svg>
            </button>
            <button type="button" class="icon-action-button danger-action" data-vault-proof-remove="${index}" aria-label="Remove evidence item ${index + 1}" title="Remove item">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 7h14M9 7V4h6v3m-7 4v7m4-7v7m4-7v7M7 7l1 14h8l1-14"></path></svg>
            </button>
          </div>
        </div>
        <textarea id="vault-proof-point-${index}" data-vault-proof-value="${index}" rows="3" maxlength="5000" aria-label="Evidence item ${index + 1}">${escapeHtml(point)}</textarea>
      </article>
    `).join("")
    : '<div class="empty-state compact">No evidence items yet. Add a source-backed responsibility or achievement.</div>';
}

function addVaultProofPoint() {
  const additions = evidenceItemsFromInput(elements.vaultProofPointInput.value);
  if (!additions.length) {
    elements.vaultProofPointInput.focus();
    return;
  }
  state.vaultProofPoints.push(...additions);
  elements.vaultProofPointInput.value = "";
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(
    `[data-vault-proof-value="${state.vaultProofPoints.length - 1}"]`,
  )?.focus();
}

function updateVaultProofPoint(index, value) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofPoints[index] = value;
  state.vaultFormDirty = true;
  const count = currentVaultProofPoints().length;
  elements.vaultProofPointCount.textContent = `${count} item${count === 1 ? "" : "s"}`;
}

function moveVaultProofPoint(index, direction) {
  const targetIndex = direction === "up" ? index - 1 : index + 1;
  if (index < 0 || targetIndex < 0 || targetIndex >= state.vaultProofPoints.length) return;
  state.vaultProofPoints = moveEvidenceItem(state.vaultProofPoints, index, direction);
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-value="${targetIndex}"]`)?.focus();
}

function removeVaultProofPoint(index) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofPoints = removeEvidenceItem(state.vaultProofPoints, index);
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-value="${Math.max(0, index - 1)}"]`)?.focus()
    || elements.vaultProofPointInput.focus();
}

function updateArchiveScopeToggle(button, showingArchived, noun) {
  if (!button) return;
  const label = showingArchived ? `Show active ${noun}` : `Show archived ${noun}`;
  button.classList.toggle("active", showingArchived);
  button.setAttribute("aria-pressed", String(showingArchived));
  button.setAttribute("aria-label", label);
  button.dataset.tooltip = `${label}.`;
}

function populateVaultExperienceForm(record) {
  const isNew = state.vaultEditorMode === "new";
  elements.vaultEditorSource.textContent = isNew
    ? "Manual entry"
    : (record?.source === "manual" ? "Manual record" : "Canonical imported record");
  elements.vaultEditorTitle.textContent = isNew
    ? "New experience"
    : ([record?.company, record?.role].filter(Boolean).join(" / ") || "Experience details");
  elements.vaultEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.vaultCompany.value = record?.company || "";
  elements.vaultRole.value = record?.role || "";
  experienceDateRange.setValue(record || {});
  elements.vaultCategory.value = record?.category || "";
  setManualSkillsFromRecord("experience", record);
  state.vaultProofPoints = [...(record?.proof_points || [])];
  elements.vaultProofPointInput.value = "";
  renderVaultProofPointEditor();
  const archived = record?.archived === true;
  elements.archiveVaultButton.hidden = isNew || !record;
  setArchiveActionButton(elements.archiveVaultButton, archived, "experience");
  elements.archiveVaultButton.dataset.tooltip = archived
    ? "Return this experience to active matching and resume workflows."
    : "Move this experience out of active matching while preserving the complete record.";
  state.vaultFormDirty = false;
}

function renderVault() {
  renderVaultCategoryOptions();
  renderVaultExperienceList();
  if (state.vaultEditorMode === "new") return;
  let record = selectedVaultExperience();
  const statusRecords = statusVaultExperiences();
  if (!record || (record.archived === true) !== (state.vaultVisibility === "archived")) {
    state.selectedExperienceId = statusRecords[0]?.id || null;
    record = statusRecords[0] || null;
    renderVaultExperienceList();
  }
  populateVaultExperienceForm(record);
}

const vaultPendingLoads = new Map();

// A late initial load must never turn a newly opened draft back into an edit of
// the previously selected record. Keep the whole editor surface non-interactive
// until all of its current loads settle; navigation outside it remains usable.
async function withVaultLoading(viewName, load) {
  const view = document.getElementById(`view-${viewName}`);
  vaultPendingLoads.set(viewName, (vaultPendingLoads.get(viewName) || 0) + 1);
  view.inert = true;
  view.setAttribute("aria-busy", "true");
  try {
    return await load();
  } finally {
    const pending = vaultPendingLoads.get(viewName) - 1;
    vaultPendingLoads.set(viewName, pending);
    if (!pending) {
      view.inert = false;
      view.removeAttribute("aria-busy");
    }
  }
}

async function loadVaultExperiences({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("vault", async () => {
    if (!state.skillsRegistry || refresh) await loadSkillsRegistry();
    const payload = (await getVaultSnapshot({ refresh })).experiences;
    state.vaultExperiences = payload.records || [];
    state.vaultCategories = payload.categories || [];
    state.vaultRevision = payload.revision;
    const visibleRecords = statusVaultExperiences();
    if (!visibleRecords.some((record) => record.id === state.selectedExperienceId)) {
      state.selectedExperienceId = visibleRecords[0]?.id || null;
    }
    state.vaultEditorMode = state.selectedExperienceId ? "edit" : "new";
    renderVault();
    if (!keepMessage) elements.vaultMessage.textContent = "";
  });
}

function beginNewExperience() {
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.vaultVisibility = "active";
  state.vaultEditorMode = "new";
  state.selectedExperienceId = null;
  populateVaultExperienceForm(null);
  renderVaultExperienceList();
  openCareerVaultCardDetail("experience");
  elements.vaultCompany.focus();
}

function selectVaultExperience(recordId) {
  if (recordId === state.selectedExperienceId && state.vaultEditorMode === "edit") {
    openCareerVaultCardDetail("experience");
    return;
  }
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.selectedExperienceId = recordId;
  state.vaultEditorMode = "edit";
  populateVaultExperienceForm(selectedVaultExperience());
  renderVaultExperienceList();
  openCareerVaultCardDetail("experience");
}

function cancelVaultEdit() {
  if (state.vaultEditorMode === "new") {
    state.selectedExperienceId = state.vaultExperiences[0]?.id || null;
    state.vaultEditorMode = state.selectedExperienceId ? "edit" : "new";
  }
  populateVaultExperienceForm(selectedVaultExperience());
  renderVaultExperienceList();
  elements.vaultMessage.textContent = "Unsaved changes discarded.";
}

function setVaultVisibility(visibility) {
  if (visibility === state.vaultVisibility) return;
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.vaultVisibility = visibility;
  state.vaultEditorMode = "edit";
  state.selectedExperienceId = statusVaultExperiences()[0]?.id || null;
  closeCareerVaultCardDetail("experience");
  renderVault();
}

async function toggleVaultExperienceArchive() {
  const record = selectedVaultExperience();
  if (!record || state.vaultEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(
    `Archive ${record.company} / ${record.role}? It will be excluded from active matching.`
  )) return;
  elements.archiveVaultButton.disabled = true;
  elements.vaultMessage.className = "form-message";
  elements.vaultMessage.textContent = restoring ? "Restoring experience..." : "Archiving experience...";
  try {
    const result = await requestJson(
      `/api/vault/experiences/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.vaultRevision }),
      }
    );
    state.vaultRevision = result.revision;
    state.selectedExperienceId = null;
    state.vaultFormDirty = false;
    await loadVaultExperiences({ keepMessage: true, refresh: true });
    elements.vaultMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.vaultMessage.className = "form-message error";
    elements.vaultMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveVaultButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultExperience(event) {
  event.preventDefault();
  const creating = state.vaultEditorMode === "new";
  const recordId = state.selectedExperienceId;
  elements.saveVaultButton.disabled = true;
  elements.saveVaultButton.textContent = "Saving...";
  elements.vaultMessage.className = "form-message";
  elements.vaultMessage.textContent = "Validating canonical Career Vault...";
  const dates = experienceDateRange.value();
  const record = {
    company: elements.vaultCompany.value,
    role: elements.vaultRole.value,
    ...dates,
    category: elements.vaultCategory.value,
    ...manualSkillRecordFields("experience"),
    proof_points: currentVaultProofPoints(),
  };
  try {
    const result = await requestJson(
      creating
        ? "/api/vault/experiences"
        : `/api/vault/experiences/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          expected_revision: state.vaultRevision,
          record,
        }),
      }
    );
    state.selectedExperienceId = result.record.id;
    if (result.registry_revision) state.skillsRegistry.revision = result.registry_revision;
    state.vaultFormDirty = false;
    await loadVaultExperiences({ keepMessage: true, refresh: true });
    elements.vaultMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.vaultMessage.className = "form-message error";
    elements.vaultMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveVaultButton.disabled = false;
    elements.saveVaultButton.textContent = "Save experience";
    await pollLogs();
  }
}

function selectedVaultProject() {
  return state.vaultProjects.find((record) => record.id === state.selectedProjectId) || null;
}

function renderProjectCategoryOptions() {
  const selectedFilter = elements.projectCategoryFilter.value;
  elements.projectCategoryFilter.innerHTML = '<option value="">All categories</option>'
    + state.projectCategories.map(
      (category) => `<option value="${escapeHtml(category)}" translate="no">${escapeHtml(category)}</option>`,
    ).join("");
  if (state.projectCategories.includes(selectedFilter)) {
    elements.projectCategoryFilter.value = selectedFilter;
  }
  elements.projectCategoryOptions.innerHTML = state.projectCategories.map(
    (category) => `<option value="${escapeHtml(category)}"></option>`,
  ).join("");
}

function statusVaultProjects() {
  const archived = state.projectVisibility === "archived";
  return state.vaultProjects.filter((record) => (record.archived === true) === archived);
}

function filteredVaultProjects() {
  const query = elements.projectSearch.value.trim().toLocaleLowerCase();
  const category = elements.projectCategoryFilter.value;
  return statusVaultProjects().filter((record) => {
    if (category && record.category !== category) return false;
    if (!query) return true;
    return [record.name, record.category, record.summary, ...(record.technologies || [])]
      .join(" ")
      .toLocaleLowerCase()
      .includes(query);
  });
}

function renderProjectList() {
  const records = filteredVaultProjects();
  const statusRecords = statusVaultProjects();
  const activeCount = state.vaultProjects.filter((record) => record.archived !== true).length;
  const archivedCount = state.vaultProjects.length - activeCount;
  elements.projectArchivedCount.textContent = archivedCount;
  updateArchiveScopeToggle(
    elements.projectArchivedFilter,
    state.projectVisibility === "archived",
    "projects",
  );
  elements.projectRecordCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.projectRecordList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedProjectId;
      return `<button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-project-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open this canonical project in the editor.">
          <span class="vault-card-kind vault-card-only">Project</span>
          <span class="vault-record-main">
            <strong>${escapeHtml(record.name)}</strong>
            <span>${escapeHtml(record.summary || "No summary")}</span>
          </span>
          <span class="vault-record-meta"><span>${escapeHtml(record.category)}</span><span>${escapeHtml(record.dates || "Dates not set")}</span></span>
          ${renderVaultCardPreview(record.technologies, "No technologies listed")}
          <span class="vault-record-counts">${(record.technologies || []).length} technologies / ${(record.proof_points || []).length} evidence items / ${(record.related_experience || []).length} experience links${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : `<div class="empty-state">No ${state.projectVisibility} projects match these filters.</div>`;
}

function renderProjectExperienceOptions(selectedIds = []) {
  const selected = new Set(selectedIds);
  elements.projectRelatedExperience.innerHTML = state.projectExperienceOptions.length
    ? state.projectExperienceOptions.map((option) => {
      const label = [option.company, option.role].filter(Boolean).join(" / ") || option.id;
      return `<label class="vault-relation-option">
        <input type="checkbox" value="${escapeHtml(option.id)}" ${selected.has(option.id) ? "checked" : ""}>
        <span><strong>${escapeHtml(label)}</strong><small>${escapeHtml(option.id)}${option.archived ? " / archived" : ""}</small></span>
      </label>`;
    }).join("")
    : '<div class="empty-state compact">No Experience records are available.</div>';
}

function populateProjectForm(record) {
  const isNew = state.projectEditorMode === "new";
  elements.projectEditorSource.textContent = isNew
    ? "New manual record"
    : (record?.source === "manual" ? "Manual record" : "Canonical record");
  elements.projectEditorTitle.textContent = isNew ? "New project" : (record?.name || "Project details");
  elements.projectEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.projectName.value = record?.name || "";
  elements.projectCategory.value = record?.category || "";
  elements.projectSummary.value = record?.summary || "";
  projectDateRange.setValue(record || {});
  setManualSkillsFromRecord("project", record);
  renderProjectExperienceOptions(record?.related_experience || []);
  elements.projectProofPoints.value = (record?.proof_points || []).join("\n");
  elements.archiveProjectButton.hidden = isNew || !record;
  if (!isNew && record) {
    const restoring = record.archived === true;
    setArchiveActionButton(elements.archiveProjectButton, restoring, "project");
    elements.archiveProjectButton.dataset.tooltip = restoring
      ? "Return this project to active matching and resume workflows."
      : "Move this project out of active workflows while preserving the complete record.";
  }
  state.projectFormDirty = false;
}

function renderProjects() {
  renderProjectCategoryOptions();
  renderProjectList();
  if (state.projectEditorMode === "new") return;
  let record = selectedVaultProject();
  const statusRecords = statusVaultProjects();
  if (!record || (record.archived === true) !== (state.projectVisibility === "archived")) {
    state.selectedProjectId = statusRecords[0]?.id || null;
    record = selectedVaultProject();
    renderProjectList();
  }
  populateProjectForm(record);
}

async function loadVaultProjects({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("projects", async () => {
    if (!state.skillsRegistry || refresh) await loadSkillsRegistry();
    const payload = (await getVaultSnapshot({ refresh })).projects;
    state.vaultProjects = payload.records || [];
    state.projectCategories = payload.categories || [];
    state.projectExperienceOptions = payload.experience_options || [];
    state.projectRevision = payload.revision;
    const visibleRecords = statusVaultProjects();
    if (!visibleRecords.some((record) => record.id === state.selectedProjectId)) {
      state.selectedProjectId = visibleRecords[0]?.id || null;
    }
    state.projectEditorMode = state.selectedProjectId ? "edit" : "new";
    renderProjects();
    if (!keepMessage) elements.projectMessage.textContent = "";
  });
}

function beginNewProject() {
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.projectVisibility = "active";
  state.projectEditorMode = "new";
  state.selectedProjectId = null;
  populateProjectForm(null);
  renderProjectList();
  openCareerVaultCardDetail("projects");
  elements.projectName.focus();
}

function selectVaultProject(recordId) {
  if (recordId === state.selectedProjectId && state.projectEditorMode === "edit") {
    openCareerVaultCardDetail("projects");
    return;
  }
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.selectedProjectId = recordId;
  state.projectEditorMode = "edit";
  populateProjectForm(selectedVaultProject());
  renderProjectList();
  openCareerVaultCardDetail("projects");
}

function cancelProjectEdit() {
  if (state.projectEditorMode === "new") {
    state.selectedProjectId = statusVaultProjects()[0]?.id || null;
    state.projectEditorMode = state.selectedProjectId ? "edit" : "new";
  }
  populateProjectForm(selectedVaultProject());
  renderProjectList();
  elements.projectMessage.textContent = "Unsaved changes discarded.";
}

function setProjectVisibility(visibility) {
  if (visibility === state.projectVisibility) return;
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.projectVisibility = visibility;
  state.projectEditorMode = "edit";
  state.selectedProjectId = statusVaultProjects()[0]?.id || null;
  closeCareerVaultCardDetail("projects");
  renderProjects();
}

async function toggleVaultProjectArchive() {
  const record = selectedVaultProject();
  if (!record || state.projectEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(`Archive ${record.name}? The record will be excluded from new matching.`)) return;
  elements.archiveProjectButton.disabled = true;
  elements.saveProjectButton.disabled = true;
  elements.projectMessage.className = "form-message";
  elements.projectMessage.textContent = restoring ? "Restoring project..." : "Archiving project...";
  try {
    const result = await requestJson(
      `/api/vault/projects/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.projectRevision }),
      },
    );
    state.projectRevision = result.revision;
    state.selectedProjectId = null;
    state.projectFormDirty = false;
    await loadVaultProjects({ keepMessage: true, refresh: true });
    elements.projectMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.projectMessage.className = "form-message error";
    elements.projectMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveProjectButton.disabled = false;
    elements.saveProjectButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultProject(event) {
  event.preventDefault();
  const creating = state.projectEditorMode === "new";
  const recordId = state.selectedProjectId;
  elements.saveProjectButton.disabled = true;
  elements.saveProjectButton.textContent = "Saving...";
  elements.projectMessage.className = "form-message";
  elements.projectMessage.textContent = "Validating canonical Career Vault...";
  const dates = projectDateRange.value();
  const record = {
    name: elements.projectName.value,
    category: elements.projectCategory.value,
    summary: elements.projectSummary.value,
    ...dates,
    ...manualSkillRecordFields("project"),
    related_experience: [...elements.projectRelatedExperience.querySelectorAll('input[type="checkbox"]:checked')]
      .map((input) => input.value),
    proof_points: vaultLines(elements.projectProofPoints.value),
  };
  try {
    const result = await requestJson(
      creating ? "/api/vault/projects" : `/api/vault/projects/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.projectRevision, record }),
      },
    );
    state.selectedProjectId = result.record.id;
    if (result.registry_revision) state.skillsRegistry.revision = result.registry_revision;
    state.projectFormDirty = false;
    await loadVaultProjects({ keepMessage: true, refresh: true });
    elements.projectMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.projectMessage.className = "form-message error";
    elements.projectMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveProjectButton.disabled = false;
    elements.saveProjectButton.textContent = "Save project";
    await pollLogs();
  }
}

function registrySkillIndex() {
  const index = new Map();
  (state.skillsRegistry?.groups || []).forEach((group) => {
    (group.skills || []).forEach((skill) => index.set(skill.id, { ...skill, group }));
  });
  return index;
}

function registryOriginClass(origin) {
  return origin === "core" ? "core" : "custom";
}

function registryOriginLabel(origin) {
  return origin === "core" ? "Core" : "Custom";
}

function registryOriginOwnership(origin) {
  return origin === "core" ? "built-in" : "user-defined";
}

function registryCreationLabel(skill) {
  if (skill?.origin !== "custom") return "";
  const labels = {
    manual_registry: "Added in Skills Registry",
    vault_editor: "Added with a Career Vault record",
    resume_digest: "Approved from Resume Digest",
    legacy_migration: "Imported by the legacy vocabulary migration",
    unspecified: "Creation source not specified",
  };
  const source = labels[skill.created_via] || "Legacy entry · creation source not recorded";
  if (!skill.created_at) return source;
  const parsed = new Date(skill.created_at);
  if (Number.isNaN(parsed.getTime())) return source;
  const date = new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(parsed);
  return `${source} · ${date}`;
}

function registryChevronIcon(direction = "right") {
  const path = direction === "left" ? "m15 18-6-6 6-6" : "m9 18 6-6-6-6";
  return `<svg class="registry-row-icon" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="${path}"></path></svg>`;
}

function applySkillsHubModeState() {
  const mode = state.skillsHubMode === "all" ? "all" : "groups";
  elements.skillsHubModeButtons.forEach((button) => {
    const selected = button.dataset.skillsHubMode === mode;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-selected", String(selected));
    button.tabIndex = selected ? 0 : -1;
  });
  elements.skillsHubSurfaces.forEach((surface) => {
    surface.hidden = surface.dataset.skillsHubSurface !== mode;
  });
}

function loadSkillsHubModePreference() {
  let storedMode = "";
  try {
    storedMode = window.localStorage.getItem(SKILLS_HUB_MODE_KEY) || "";
  } catch (_error) {
    storedMode = "";
  }
  state.skillsHubMode = ["groups", "all"].includes(storedMode) ? storedMode : "groups";
  applySkillsHubModeState();
}

function setSkillsHubMode(mode, { persist = true, writeHistory = true } = {}) {
  if (!["groups", "all"].includes(mode)) return;
  state.skillsHubMode = mode;
  if (persist) {
    try {
      window.localStorage.setItem(SKILLS_HUB_MODE_KEY, mode);
    } catch (_error) {
      // The page still works when browser storage is unavailable.
    }
  }
  applySkillsHubModeState();
  if (state.activeView === "skills" && writeHistory) {
    const route = localRouteForView("skills");
    if (window.location.hash !== route) {
      window.history.pushState({ careerHubView: "skills", skillsMode: mode }, "", route);
    }
  }
  if (mode === "groups") {
    renderSkillsRegistry();
  } else {
    populateCompetencyFilters();
    renderCompetencyExplorer();
  }
}

function applySkillsRegistryLayoutState() {
  if (!elements.skillsRegistryPanel) return;
  const layout = state.skillsRegistryLayout === "cards" ? "cards" : "list";
  elements.skillsRegistryPanel.dataset.registryLayout = layout;
  elements.skillsRegistryPanel.classList.toggle(
    "is-card-detail-open",
    layout === "cards" && state.registryCardDetailOpen,
  );
  elements.skillsRegistryLayoutButtons.forEach((button) => {
    const selected = button.dataset.registryLayout === layout;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-pressed", String(selected));
  });
}

function loadSkillsRegistryLayoutPreference() {
  let storedLayout = "";
  try {
    storedLayout = window.localStorage.getItem(SKILLS_REGISTRY_LAYOUT_KEY) || "";
  } catch (_error) {
    storedLayout = "";
  }
  state.skillsRegistryLayout = ["list", "cards"].includes(storedLayout) ? storedLayout : "list";
  state.registryCardDetailOpen = false;
  applySkillsRegistryLayoutState();
}

function setSkillsRegistryLayout(layout, { persist = true } = {}) {
  if (!["list", "cards"].includes(layout)) return;
  state.skillsRegistryLayout = layout;
  state.registryCardDetailOpen = false;
  state.registryGroupEditing = false;
  if (persist) {
    try {
      window.localStorage.setItem(SKILLS_REGISTRY_LAYOUT_KEY, layout);
    } catch (_error) {
      // The view still works when browser storage is unavailable.
    }
  }
  applySkillsRegistryLayoutState();
}

function registryAliasSummary(skill) {
  const aliases = skill.aliases || [];
  if (!aliases.length) return '<span class="registry-empty-value">No aliases</span>';
  const visible = aliases.slice(0, 3);
  const remaining = aliases.length - visible.length;
  return `<span title="${escapeHtml(aliases.join(", "))}">${escapeHtml(visible.join(", "))}${remaining ? ` +${remaining}` : ""}</span>`;
}

function sortRegistryGroups(groups) {
  const sorted = [...groups];
  const skillCount = (group) => (group.skills || []).filter(
    (skill) => (skill.archived === true) === (state.registryVisibility === "archived")
  ).length;
  sorted.sort((left, right) => {
    if (state.registryGroupSort === "name_desc") return right.name.localeCompare(left.name);
    if (state.registryGroupSort === "skills_desc") return skillCount(right) - skillCount(left) || left.name.localeCompare(right.name);
    if (state.registryGroupSort === "skills_asc") return skillCount(left) - skillCount(right) || left.name.localeCompare(right.name);
    if (state.registryGroupSort === "source") return left.origin.localeCompare(right.origin) || left.name.localeCompare(right.name);
    return left.name.localeCompare(right.name);
  });
  return sorted;
}

function sortRegistrySkills(skills) {
  return [...skills].sort((left, right) => {
    if (state.registrySkillSort === "name_desc") return right.name.localeCompare(left.name);
    if (state.registrySkillSort === "source") return left.origin.localeCompare(right.origin) || left.name.localeCompare(right.name);
    if (state.registrySkillSort === "aliases_desc") return (right.aliases || []).length - (left.aliases || []).length || left.name.localeCompare(right.name);
    return left.name.localeCompare(right.name);
  });
}

function renderRegistryGroupCard(group) {
  const activeSkills = (group.skills || []).filter((skill) => skill.archived !== true);
  const archivedSkills = (group.skills || []).filter((skill) => skill.archived === true).length;
  const shownSkills = group.archived
    ? (group.skills || []).filter((skill) => skill.archived === true)
    : activeSkills;
  const origin = registryOriginClass(group.origin);
  const countLabel = `${activeSkills.length} active skill${activeSkills.length === 1 ? "" : "s"}${archivedSkills ? `, ${archivedSkills} archived` : ""}`;
  const previewSkills = shownSkills.slice(0, 4);
  const preview = previewSkills.length
    ? previewSkills.map((skill) => `<span class="registry-card-skill">${escapeHtml(skill.name)}</span>`).join("")
    : '<span class="registry-card-skill is-empty">No active skills yet</span>';
  return `<button type="button" class="registry-group-card-button origin-${origin}${group.archived ? " is-archived" : ""}"
    data-registry-group-id="${escapeHtml(group.id)}" aria-label="Open ${escapeHtml(group.name)}, ${escapeHtml(countLabel)}">
    <span class="registry-card-header">
      <span class="registry-card-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(group.origin)} <small>${registryOriginOwnership(group.origin)}</small></span>
      <span class="registry-card-count" title="${escapeHtml(countLabel)}">${group.archived ? `${archivedSkills} archived` : `${activeSkills.length} active`}</span>
    </span>
    <span class="registry-card-body">
      <strong class="registry-card-title">${escapeHtml(group.name)}</strong>
      <span class="registry-card-skills">${preview}</span>
    </span>
    <span class="registry-card-footer"><span>Open group</span>${registryChevronIcon()}</span>
  </button>`;
}

function renderSkillsRegistry() {
  if (!elements.skillsRegistryGrid) return;
  applySkillsRegistryLayoutState();
  const query = (elements.skillsRegistrySearch?.value || "").trim().toLocaleLowerCase();
  const allGroups = state.skillsRegistry?.groups || [];
  const archivedGroupCount = allGroups.filter((group) => group.archived === true).length;
  const scopedGroups = allGroups.filter(
    (group) => (group.archived === true) === (state.registryVisibility === "archived")
  );
  const groups = sortRegistryGroups(scopedGroups.filter((group) => {
    if (!query) return true;
    const values = [
      group.name,
      ...(group.skills || []).flatMap((skill) => [skill.name, ...(skill.aliases || [])]),
    ];
    return values.some((value) => String(value || "").toLocaleLowerCase().includes(query));
  }));
  elements.skillsRegistryArchivedCount.textContent = archivedGroupCount;
  updateArchiveScopeToggle(
    elements.skillsRegistryArchivedFilter,
    state.registryVisibility === "archived",
    "skill groups",
  );
  if (
    groups.length
    && !groups.some((group) => group.id === state.selectedRegistryGroupId)
  ) {
    state.selectedRegistryGroupId = groups[0].id;
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
  }
  if (elements.skillsRegistryGroupCount) {
    elements.skillsRegistryGroupCount.textContent = query
      ? `${groups.length} of ${scopedGroups.length}`
      : String(scopedGroups.length);
  }
  if (!groups.length) {
    const scope = state.registryVisibility === "archived" ? "archived" : "active";
    elements.skillsRegistryGrid.innerHTML = `<div class="registry-list-empty">No ${scope} groups or skills match this view.</div>`;
    elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-empty"><strong>No matching ${scope} groups</strong><span>Try a broader skill name or switch the archive view.</span></div>`;
    return;
  }
  elements.skillsRegistryGrid.innerHTML = groups.map((group) => {
    if (state.skillsRegistryLayout === "cards") return renderRegistryGroupCard(group);
    const activeSkills = (group.skills || []).filter((skill) => skill.archived !== true);
    const archivedSkills = (group.skills || []).filter((skill) => skill.archived === true).length;
    const selected = group.id === state.selectedRegistryGroupId;
    const origin = registryOriginClass(group.origin);
    const countLabel = `${activeSkills.length} active skill${activeSkills.length === 1 ? "" : "s"}${archivedSkills ? `, ${archivedSkills} archived` : ""}`;
    return `<button type="button" class="registry-group-row origin-${origin}${selected ? " is-selected" : ""}${group.archived ? " is-archived" : ""}"
      data-registry-group-id="${escapeHtml(group.id)}" aria-current="${selected ? "page" : "false"}" aria-label="Open ${escapeHtml(group.name)}, ${escapeHtml(countLabel)}">
      <span class="registry-group-row-name"><strong>${escapeHtml(group.name)}</strong>${group.archived ? "<small>Archived</small>" : ""}</span>
      <span class="registry-group-row-count" title="${escapeHtml(countLabel)}">${group.archived ? archivedSkills : activeSkills.length}</span>
      <span class="registry-origin-text is-${origin}">${registryOriginLabel(group.origin)}</span>
      ${registryChevronIcon()}
    </button>`;
  }).join("");
  renderSkillsRegistryDetail();
}

function registryGroup(groupId) {
  return (state.skillsRegistry?.groups || []).find((group) => group.id === groupId) || null;
}

function registrySkill(skillId) {
  return registrySkillIndex().get(skillId) || null;
}

function registryGroupOptions(selectedId) {
  return (state.skillsRegistry?.groups || [])
    .filter((group) => group.archived !== true || group.id === selectedId)
    .map((group) => `<option value="${escapeHtml(group.id)}"${group.id === selectedId ? " selected" : ""} translate="no">${escapeHtml(group.name)} (${escapeHtml(group.origin)})</option>`)
    .join("");
}

function renderRegistrySkillEvidence(skill) {
  const historical = skill.archived === true;
  const projection = historical
    ? state.historicalCompetencyExplorer
    : state.competencyExplorer;
  const item = (projection?.competencies || []).find(
    (candidate) => candidate.id === skill.id
  );
  const loading = !projection;
  const evidence = item?.evidence || { experiences: [], projects: [], certifications: [] };
  const evidenceCount = Number(item?.evidence_count || 0);
  const contextCount = Number(item?.professional_context_count || 0);
  return `<section class="registry-skill-evidence" aria-label="Evidence for ${escapeHtml(skill.name)}">
    <div class="registry-skill-evidence-heading">
      <div><span>${historical ? "Historical evidence" : "Linked evidence"}</span><strong>${loading ? "Loading…" : `${evidenceCount} record${evidenceCount === 1 ? "" : "s"}`}</strong></div>
      ${historical
        ? '<span class="registry-historical-scope">Archived scope</span>'
        : `<button type="button" class="text-button" data-open-skill-evidence="${escapeHtml(skill.id)}">Open in All skills</button>`}
    </div>
    ${loading ? `<p class="competency-evidence-empty">Loading the ${historical ? "historical " : ""}evidence projection…</p>` : `
      <div class="registry-skill-evidence-metrics">
        <span><strong>${evidenceCount.toLocaleString()}</strong> evidence</span>
        <span><strong>${contextCount.toLocaleString()}</strong> professional contexts</span>
        <span><strong>${escapeHtml(competencyLastUsed(item || {}))}</strong> last used</span>
      </div>
      ${evidenceCount ? `
        ${renderCompetencyEvidenceSection("Experience", evidence.experiences || [])}
        ${renderCompetencyEvidenceSection("Projects", evidence.projects || [])}
        ${renderCompetencyEvidenceSection("Certifications", evidence.certifications || [])}
      ` : `<p class="competency-evidence-empty">No ${historical ? "historical " : ""}Career Vault evidence is linked to this registry entry.</p>`}
    `}
  </section>`;
}

function renderSkillsRegistryDetail() {
  if (!elements.skillsRegistryDetail) return;
  const selectedSkill = state.selectedRegistrySkillId
    ? registrySkill(state.selectedRegistrySkillId)
    : null;
  if (selectedSkill) {
    const parentArchived = selectedSkill.group.archived === true;
    const editable = selectedSkill.origin === "custom" && !parentArchived;
    const origin = registryOriginClass(selectedSkill.origin);
    const creationLabel = registryCreationLabel(selectedSkill);
    const header = `
      <button type="button" class="registry-back-button" data-registry-back-group="${escapeHtml(selectedSkill.primary_group_id)}">
        ${registryChevronIcon("left")}<span>${escapeHtml(selectedSkill.group.name)}</span>
      </button>
      <header class="registry-detail-header registry-skill-detail-header">
        <div>
          <span class="registry-detail-eyebrow is-${origin}">${registryOriginLabel(selectedSkill.origin)} <small>${registryOriginOwnership(selectedSkill.origin)}</small></span>
          <h2>${escapeHtml(selectedSkill.name)}</h2>
          <p><code>${escapeHtml(selectedSkill.id)}</code>${selectedSkill.archived ? " <span class=\"registry-archived-label\">Archived</span>" : ""}</p>
          ${creationLabel ? `<p class="registry-provenance">${escapeHtml(creationLabel)}</p>` : ""}
        </div>
      </header>`;
    if (!editable) {
      const managedNote = selectedSkill.origin === "core"
        ? "Core vocabulary is maintained by Career Hub and can be extended with your own custom skills."
        : "This custom skill is locked while its group is archived. Restore the group to edit or restore the skill.";
      elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-shell">
        ${header}
        <dl class="registry-readonly-fields">
          <div><dt>Canonical name</dt><dd>${escapeHtml(selectedSkill.name)}</dd></div>
          <div><dt>Aliases</dt><dd>${(selectedSkill.aliases || []).length ? escapeHtml(selectedSkill.aliases.join(", ")) : "None"}</dd></div>
          <div><dt>Primary group</dt><dd>${escapeHtml(selectedSkill.group.name)}</dd></div>
        </dl>
        <p class="registry-managed-note">${managedNote}</p>
        ${renderRegistrySkillEvidence(selectedSkill)}
      </div>`;
      return;
    }
    elements.skillsRegistryDetail.innerHTML = `<form class="registry-detail-form registry-detail-shell" data-registry-detail-form="skill" data-registry-record-id="${escapeHtml(selectedSkill.id)}">
      ${header}
      <div class="registry-editor-grid">
        <label class="editor-field"><span>Canonical name</span><input name="name" maxlength="160" value="${escapeHtml(selectedSkill.name)}" required></label>
        <label class="editor-field"><span>Primary group</span><select name="primary_group_id" required>${registryGroupOptions(selectedSkill.primary_group_id)}</select></label>
        <label class="editor-field registry-editor-wide"><span>Aliases</span><input name="aliases" value="${escapeHtml((selectedSkill.aliases || []).join(", "))}" placeholder="Comma-separated exact aliases"></label>
      </div>
      <div class="registry-detail-actions">
        <button type="button" class="secondary-button registry-danger-button" data-registry-archive-kind="skill" data-registry-archive-id="${escapeHtml(selectedSkill.id)}" data-registry-archived="${selectedSkill.archived ? "true" : "false"}">${selectedSkill.archived ? "Restore skill" : "Archive skill"}</button>
        <button type="submit" class="primary-button">Save changes</button>
      </div>
      ${renderRegistrySkillEvidence(selectedSkill)}
    </form>`;
    return;
  }

  const selectedGroup = registryGroup(state.selectedRegistryGroupId);
  if (!selectedGroup) {
    elements.skillsRegistryDetail.innerHTML = '<div class="empty-state">Select a group or skill to inspect and manage it.</div>';
    return;
  }
  const editable = selectedGroup.origin === "custom";
  const skills = selectedGroup.skills || [];
  const detailQuery = (elements.skillsRegistrySearch?.value || "").trim().toLocaleLowerCase();
  const groupMatchesQuery = detailQuery
    && String(selectedGroup.name || "").toLocaleLowerCase().includes(detailQuery);
  const scopedSkills = skills.filter(
    (skill) => (skill.archived === true) === (state.registrySkillVisibility === "archived")
  );
  const matchingDetailSkills = detailQuery && !groupMatchesQuery
    ? scopedSkills.filter((skill) => (
      [skill.name, ...(skill.aliases || [])].some(
        (value) => String(value || "").toLocaleLowerCase().includes(detailQuery)
      )
    ))
    : scopedSkills;
  const sortedDetailSkills = sortRegistrySkills(matchingDetailSkills);
  const visibleDetailSkills = sortedDetailSkills.slice(0, 100);
  const activeSkillCount = skills.filter((skill) => !skill.archived).length;
  const archivedSkillCount = skills.length - activeSkillCount;
  const origin = registryOriginClass(selectedGroup.origin);
  const cardBack = state.skillsRegistryLayout === "cards" ? `
    <button type="button" class="registry-back-button registry-all-groups-button" data-close-registry-card-detail>
      ${registryChevronIcon("left")}<span>All groups</span>
    </button>` : "";
  const editForm = editable && state.registryGroupEditing ? `
    <form class="registry-group-edit-form" data-registry-detail-form="group" data-registry-record-id="${escapeHtml(selectedGroup.id)}">
      <label class="editor-field"><span>Group name</span><input name="name" maxlength="160" value="${escapeHtml(selectedGroup.name)}" required></label>
      <div class="registry-group-edit-actions">
        <button type="button" class="secondary-button" data-cancel-registry-group-edit>Cancel</button>
        <button type="submit" class="primary-button">Save group</button>
      </div>
    </form>` : "";
  elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-shell registry-group-detail">
    ${cardBack}
    <header class="registry-detail-header">
      <div>
        <span class="registry-detail-eyebrow is-${origin}">${registryOriginLabel(selectedGroup.origin)} <small>${registryOriginOwnership(selectedGroup.origin)}</small></span>
        <h2>${escapeHtml(selectedGroup.name)}</h2>
        <p><code>${escapeHtml(selectedGroup.id)}</code> <span aria-hidden="true">&middot;</span> ${activeSkillCount} active skill${activeSkillCount === 1 ? "" : "s"}${selectedGroup.archived ? " <span class=\"registry-archived-label\">Archived</span>" : ""}</p>
      </div>
      <div class="registry-header-actions">
        ${editable ? `<button type="button" class="secondary-button icon-action-button registry-danger-button" data-registry-archive-kind="group" data-registry-archive-id="${escapeHtml(selectedGroup.id)}" data-registry-archived="${selectedGroup.archived ? "true" : "false"}" aria-label="${selectedGroup.archived ? "Restore" : "Archive"} ${escapeHtml(selectedGroup.name)}" data-tooltip="${selectedGroup.archived ? "Restore this group and skills archived with it." : "Archive this group and cascade archive its active skills."}"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg></button>` : ""}
        ${editable ? `<button type="button" class="secondary-button" data-edit-registry-group ${state.registryGroupEditing ? "disabled" : ""}>Edit group</button>` : ""}
        <button type="button" class="primary-button" data-add-skill-to-group="${escapeHtml(selectedGroup.id)}" ${selectedGroup.archived ? "disabled" : ""}>Add skill</button>
      </div>
    </header>
    ${editForm}
    <div class="registry-skill-toolbar">
      <label class="compact-select-field"><span class="visually-hidden">Sort skills</span><select data-registry-skill-sort aria-label="Sort skills"><option value="name_asc" ${state.registrySkillSort === "name_asc" ? "selected" : ""}>Name A–Z</option><option value="name_desc" ${state.registrySkillSort === "name_desc" ? "selected" : ""}>Name Z–A</option><option value="source" ${state.registrySkillSort === "source" ? "selected" : ""}>Source</option><option value="aliases_desc" ${state.registrySkillSort === "aliases_desc" ? "selected" : ""}>Most aliases</option></select></label>
      <button type="button" class="filter-button archive-filter-button${state.registrySkillVisibility === "archived" ? " active" : ""}" data-registry-skills-archive-filter aria-label="${state.registrySkillVisibility === "archived" ? "Show active skills" : "Show archived skills"}" aria-pressed="${state.registrySkillVisibility === "archived"}" data-tooltip="${state.registrySkillVisibility === "archived" ? "Show active skills." : "Show archived skills."}"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg><span>${archivedSkillCount}</span></button>
    </div>
    <div class="registry-skill-table" aria-label="Skills in ${escapeHtml(selectedGroup.name)}">
      <div class="registry-skill-table-header">
        <span>Skill</span>
        <span>Aliases</span>
        <span>Source</span>
        <span aria-hidden="true"></span>
      </div>
      <div class="registry-detail-skill-list">
        ${visibleDetailSkills.length ? visibleDetailSkills.map((skill) => {
          const skillOrigin = registryOriginClass(skill.origin);
          return `<button type="button" class="registry-detail-skill${skill.archived ? " is-archived" : ""}" data-registry-skill-id="${escapeHtml(skill.id)}" aria-label="Open ${escapeHtml(skill.name)} skill details">
            <strong>${escapeHtml(skill.name)}${skill.archived ? '<small class="registry-archived-label">Archived</small>' : ""}</strong>
            <span class="registry-alias-cell">${registryAliasSummary(skill)}</span>
            <span class="registry-origin-text is-${skillOrigin}">${registryOriginLabel(skill.origin)}</span>
            ${registryChevronIcon()}
          </button>`;
        }).join("") : '<div class="registry-detail-empty"><strong>No matching skills</strong><span>Try a broader search or add a new skill to this group.</span></div>'}
      </div>
      ${sortedDetailSkills.length > visibleDetailSkills.length ? `<p class="registry-list-limit">Showing ${visibleDetailSkills.length} of ${sortedDetailSkills.length}. Narrow the search to find another skill.</p>` : ""}
    </div>
  </div>`;
}

async function saveSkillsRegistryDetail(event) {
  const form = event.target.closest("[data-registry-detail-form]");
  if (!form || !state.skillsRegistry) return;
  event.preventDefault();
  const formData = new FormData(form);
  const recordId = form.dataset.registryRecordId;
  elements.skillsRegistryMessage.className = "form-message";
  elements.skillsRegistryMessage.textContent = "Validating private Skills Registry...";
  try {
    if (form.dataset.registryDetailForm === "group") {
      await api.updateSkillGroup(recordId, state.skillsRegistry.revision, {
        name: String(formData.get("name") || ""),
      });
      state.selectedRegistryGroupId = recordId;
      state.registryGroupEditing = false;
    } else {
      const primaryGroupId = String(formData.get("primary_group_id") || "");
      await api.updateRegistrySkill(recordId, state.skillsRegistry.revision, {
        name: String(formData.get("name") || ""),
        aliases: vaultLines(formData.get("aliases"), true),
        primary_group_id: primaryGroupId,
      });
      state.selectedRegistrySkillId = recordId;
      state.selectedRegistryGroupId = primaryGroupId;
    }
    await loadSkillsRegistry({ keepMessage: true });
    await loadCompetencyExplorer({ force: true }).catch(() => null);
    elements.skillsRegistryMessage.textContent = "Custom registry entry saved in your local Data Home.";
  } catch (error) {
    elements.skillsRegistryMessage.className = "form-message error";
    elements.skillsRegistryMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "SKILL_REGISTRY_REVISION_CONFLICT") {
      await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
    }
  }
}

async function toggleSkillsRegistryArchive(kind, recordId, archived) {
  if (!state.skillsRegistry) return;
  const verb = archived ? "restore" : "archive";
  let confirmation = `${verb.charAt(0).toUpperCase() + verb.slice(1)} this custom ${kind}?`;
  if (kind === "group") {
    const group = registryGroup(recordId);
    const activeCount = (group?.skills || []).filter((skill) => !skill.archived).length;
    const cascadeCount = (group?.cascade_archived_skill_ids || []).length;
    confirmation = archived
      ? `Restore ${group?.name || "this group"}? ${cascadeCount} skill${cascadeCount === 1 ? "" : "s"} archived with the group will be restored. Skills archived separately will stay archived.`
      : `Archive ${group?.name || "this group"}? This will also archive ${activeCount} active skill${activeCount === 1 ? "" : "s"} in the group. Existing evidence and history stay linked, but these skills will be excluded from new intake and matching.`;
  }
  if (!window.confirm(confirmation)) return;
  try {
    let result;
    if (kind === "group") {
      result = await api.setSkillGroupArchived(recordId, !archived, state.skillsRegistry.revision);
      state.selectedRegistryGroupId = recordId;
      state.selectedRegistrySkillId = null;
      state.registryGroupEditing = false;
      state.registryVisibility = archived ? "active" : "archived";
      state.registrySkillVisibility = archived ? "active" : "archived";
    } else {
      result = await api.setRegistrySkillArchived(recordId, !archived, state.skillsRegistry.revision);
      state.selectedRegistrySkillId = recordId;
      state.registrySkillVisibility = archived ? "active" : "archived";
    }
    historicalCompetencyExplorerLoaded = false;
    state.historicalCompetencyExplorer = null;
    await loadSkillsRegistry({ keepMessage: true });
    await loadCompetencyExplorer({ force: true }).catch(() => null);
    const affected = Number(result?.affected_skill_count || 0);
    elements.skillsRegistryMessage.textContent = kind === "group"
      ? `Group ${archived ? "restored" : "archived"}. ${affected} skill${affected === 1 ? "" : "s"} ${archived ? "restored with it" : "archived with it"}.`
      : `Skill ${archived ? "restored" : "archived"}.`;
  } catch (error) {
    elements.skillsRegistryMessage.className = "form-message error";
    elements.skillsRegistryMessage.textContent = formatRequestError(error);
  }
}

function populateSkillQuickGroups(preferredGroupId = "") {
  const groups = (state.skillsRegistry?.groups || []).filter((group) => group.archived !== true);
  const effectiveGroupId = preferredGroupId || state.skillsRegistry?.default_custom_group_id || "";
  elements.skillQuickGroup.innerHTML = groups.map((group) => (
    `<option value="${escapeHtml(group.id)}" translate="no">${escapeHtml(group.name)} (${escapeHtml(group.origin)})</option>`
  )).join("");
  if (effectiveGroupId && groups.some((group) => group.id === effectiveGroupId)) {
    elements.skillQuickGroup.value = effectiveGroupId;
  }
}

async function loadSkillsRegistry({ keepMessage = false } = {}) {
  try {
    state.skillsRegistry = await api.getSkillsRegistry();
    const groups = state.skillsRegistry.groups || [];
    if (state.selectedRegistrySkillId && !registrySkill(state.selectedRegistrySkillId)) {
      state.selectedRegistrySkillId = null;
    }
    if (!state.selectedRegistryGroupId || !registryGroup(state.selectedRegistryGroupId)) {
      state.selectedRegistryGroupId = groups.find(
        (group) => (group.archived === true) === (state.registryVisibility === "archived")
      )?.id
        || state.skillsRegistry.default_custom_group_id
        || null;
    }
    renderSkillsRegistry();
    populateSkillQuickGroups(elements.skillQuickGroup?.value || "");
    if (state.competencyExplorer) {
      populateCompetencyFilters();
      renderCompetencyExplorer();
    }
    loadHistoricalEvidenceIfNeeded(registrySkill(state.selectedRegistrySkillId));
    if (!keepMessage && elements.skillsRegistryMessage) {
      const notices = state.skillsRegistry.compatibility_notices || [];
      const activeCount = groups
        .filter((group) => !group.archived)
        .reduce(
          (count, group) => count + (group.skills || []).filter((skill) => !skill.archived).length,
          0,
        );
      elements.skillsRegistryMessage.className = notices.length
        ? "form-message warning"
        : "form-message";
      elements.skillsRegistryMessage.textContent = notices.length
        ? `${activeCount} active registry entries. ${notices.length} catalog compatibility adjustment${notices.length === 1 ? " was" : "s were"} applied without changing private vocabulary.`
        : `${activeCount} active registry entries available to both intake workflows.`;
    }
    return state.skillsRegistry;
  } catch (error) {
    if (elements.skillsRegistryMessage) {
      elements.skillsRegistryMessage.className = "form-message error";
      elements.skillsRegistryMessage.textContent = formatRequestError(error);
    }
    throw error;
  }
}

function competencyLabel(value) {
  if (!value) return "Unclassified";
  return String(value)
    .replaceAll("_", " ")
    .replace(/\b\w/g, (character) => character.toLocaleUpperCase());
}

function competencyLastUsed(item) {
  if (item.active_now) return "Active now";
  if (!item.last_used) return "Not dated";
  const [year, month] = String(item.last_used).split("-").map(Number);
  if (!year || !month) return String(item.last_used);
  return new Intl.DateTimeFormat(undefined, { month: "short", year: "numeric" })
    .format(new Date(Date.UTC(year, month - 1, 1)));
}

function allSkillRecords() {
  const evidenceById = new Map(
    (state.competencyExplorer?.competencies || []).map((item) => [item.id, item])
  );
  const records = [];
  (state.skillsRegistry?.groups || []).forEach((group) => {
    if (group.archived === true) return;
    (group.skills || []).forEach((skill) => {
      if (skill.archived === true) return;
      const evidenceItem = evidenceById.get(skill.id) || {};
      records.push({
        ...evidenceItem,
        ...skill,
        primary_group_id: skill.primary_group_id || group.id,
        group_name: group.name,
        group_origin: group.origin,
        primary_domain: skill.primary_domain || evidenceItem.primary_domain || "",
        kind: skill.kind || evidenceItem.kind || "",
        evidence_count: Number(evidenceItem.evidence_count || 0),
        professional_context_count: Number(evidenceItem.professional_context_count || 0),
        project_context_count: Number(evidenceItem.project_context_count || 0),
        certification_count: Number(evidenceItem.certification_count || 0),
        proof_count: Number(evidenceItem.proof_count || 0),
        experience_months: Number(evidenceItem.experience_months || 0),
        first_used: evidenceItem.first_used || null,
        last_used: evidenceItem.last_used || null,
        active_now: evidenceItem.active_now === true,
        evidence: evidenceItem.evidence || { experiences: [], projects: [], certifications: [] },
      });
    });
  });
  return records.sort((left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id));
}

function selectedCompetency() {
  return allSkillRecords().find(
    (item) => item.id === state.selectedCompetencyId
  ) || null;
}

function filteredCompetencies() {
  const query = (elements.competencySearch?.value || "").trim().toLocaleLowerCase();
  const group = elements.competencyGroupFilter?.value || "";
  const origin = elements.competencyOriginFilter?.value || "";
  const evidence = elements.competencyEvidenceFilter?.value || "";
  const domain = elements.competencyDomainFilter?.value || "";
  const kind = elements.competencyKindFilter?.value || "";
  return allSkillRecords().filter((item) => {
    if (group && item.primary_group_id !== group) return false;
    if (origin && item.origin !== origin) return false;
    if (evidence === "with" && !item.evidence_count) return false;
    if (evidence === "without" && item.evidence_count) return false;
    if (domain && item.primary_domain !== domain) return false;
    if (kind && item.kind !== kind) return false;
    if (!query) return true;
    return [item.name, item.id, item.group_name, ...(item.aliases || [])]
      .some((value) => String(value || "").toLocaleLowerCase().includes(query));
  });
}

function renderCompetencySummary() {
  if (!elements.competencySummary) return;
  const records = allSkillRecords();
  const summary = state.competencyExplorer?.summary || {};
  const metrics = [
    [records.length, "active registry entries"],
    [records.filter((item) => item.evidence_count > 0).length, "with evidence"],
    [summary.professional_context_count || 0, "professional contexts"],
    [records.filter((item) => item.active_now).length, "active now"],
  ];
  elements.competencySummary.innerHTML = metrics.map(([value, label]) => (
    `<div><strong>${Number(value).toLocaleString()}</strong><span>${escapeHtml(label)}</span></div>`
  )).join("");
}

function renderCompetencyEvidenceSection(title, items) {
  const rows = (items || []).map((item) => {
    const relationship = item.direct ? "Direct" : "Via project";
    const context = item.context || (
      item.is_independent_project ? "Independent project" : "Canonical Career Vault record"
    );
    return `<div class="competency-evidence-row">
      <div class="competency-evidence-copy">
        <strong>${escapeHtml(item.title)}</strong>
        <span>${escapeHtml(context)}</span>
      </div>
      <span class="competency-evidence-relationship">${relationship}</span>
      <button type="button" class="text-button competency-open-record" data-competency-record-view="${escapeHtml(item.view)}" data-competency-record-id="${escapeHtml(item.record_id)}">
        <span>Open record</span><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 12h13m-5-5 5 5-5 5"></path></svg>
      </button>
    </div>`;
  }).join("");
  return `<section class="competency-evidence-section">
    <div class="competency-evidence-heading"><h3>${escapeHtml(title)}</h3><span>${items.length}</span></div>
    ${rows || '<p class="competency-evidence-empty">No linked records.</p>'}
  </section>`;
}

function renderCompetencyDetail() {
  if (!elements.competencyDetail) return;
  const item = selectedCompetency();
  if (!item) {
    elements.competencyDetail.innerHTML = `<div class="competency-detail-empty">
      <strong>No skill selected</strong>
      <span>Adjust the filters or choose a skill from the list.</span>
    </div>`;
    return;
  }
  const aliases = (item.aliases || []).length
    ? item.aliases.map((alias) => `<span>${escapeHtml(alias)}</span>`).join("")
    : '<span class="is-empty">No aliases</span>';
  const months = Number(item.experience_months || 0);
  const duration = months ? `${months.toLocaleString()} month${months === 1 ? "" : "s"}` : "Not dated";
  const origin = registryOriginClass(item.origin);
  const evidence = item.evidence || { experiences: [], projects: [], certifications: [] };
  elements.competencyDetail.innerHTML = `<div class="competency-detail-shell">
    <button type="button" class="competency-list-return" data-competency-list-return>
      ${registryChevronIcon("left")}<span>All skills</span>
    </button>
    <header class="competency-detail-header">
      <div>
        <span class="competency-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(item.origin)} <small>${registryOriginOwnership(item.origin)}</small></span>
        <h2>${escapeHtml(item.name)}</h2>
        <code>${escapeHtml(item.id)}</code>
      </div>
      <div class="competency-detail-header-actions">
        ${item.active_now ? '<span class="competency-active-state"><i aria-hidden="true"></i>Active now</span>' : ""}
        <button type="button" class="secondary-button" data-manage-registry-skill="${escapeHtml(item.id)}">Manage skill</button>
      </div>
    </header>
    <div class="competency-aliases" aria-label="Aliases">${aliases}</div>
    <dl class="competency-classification">
      <div><dt>Domain</dt><dd>${escapeHtml(competencyLabel(item.primary_domain))}</dd></div>
      <div><dt>Kind</dt><dd>${escapeHtml(competencyLabel(item.kind))}</dd></div>
      <div><dt>Primary group</dt><dd class="competency-group-value"><strong>${escapeHtml(item.group_name || "Unassigned")}</strong><code>${escapeHtml(item.primary_group_id)}</code></dd></div>
    </dl>
    <div class="competency-detail-metrics">
      <div><strong>${Number(item.evidence_count || 0).toLocaleString()}</strong><span>evidence records</span></div>
      <div><strong>${Number(item.professional_context_count || 0).toLocaleString()}</strong><span>professional contexts</span></div>
      <div><strong>${escapeHtml(duration)}</strong><span>dated evidence</span></div>
      <div><strong>${escapeHtml(competencyLastUsed(item))}</strong><span>last used</span></div>
    </div>
    ${renderCompetencyEvidenceSection("Experience", evidence.experiences || [])}
    ${renderCompetencyEvidenceSection("Projects", evidence.projects || [])}
    ${renderCompetencyEvidenceSection("Certifications", evidence.certifications || [])}
  </div>`;
}

function renderCompetencyExplorer() {
  if (!elements.competencyList) return;
  renderCompetencySummary();
  const items = filteredCompetencies();
  if (!items.some((item) => item.id === state.selectedCompetencyId)) {
    state.selectedCompetencyId = items[0]?.id || null;
  }
  elements.competencyList.innerHTML = items.length ? items.map((item) => {
    const selected = item.id === state.selectedCompetencyId;
    const origin = registryOriginClass(item.origin);
    const evidenceCount = Number(item.evidence_count || 0);
    const evidenceLabel = evidenceCount
      ? `${evidenceCount} evidence record${evidenceCount === 1 ? "" : "s"}`
      : "No Career Vault evidence";
    return `<button type="button" class="competency-row${selected ? " is-selected" : ""}" role="option" aria-selected="${selected}" data-competency-id="${escapeHtml(item.id)}">
      <span class="competency-row-name"><strong>${escapeHtml(item.name)}</strong><span class="competency-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(item.origin)}</span></span>
      <span class="competency-row-classification"><strong>${escapeHtml(item.group_name || "Unassigned")}</strong><span>${escapeHtml(competencyLabel(item.kind))}</span></span>
      <span class="competency-row-evidence${evidenceCount ? "" : " is-empty"}" title="${escapeHtml(evidenceLabel)}">${evidenceCount ? evidenceCount.toLocaleString() : "No evidence"}</span>
      <span class="competency-row-last-used">${escapeHtml(competencyLastUsed(item))}</span>
      ${registryChevronIcon()}
    </button>`;
  }).join("") : `<div class="competency-list-empty">
    <strong>No skills match</strong><span>Try a broader search or remove one of the filters.</span>
  </div>`;
  const total = allSkillRecords().length;
  elements.competencyResultCount.textContent = items.length === total
    ? `${total.toLocaleString()} active registry entries`
    : `${items.length.toLocaleString()} of ${total.toLocaleString()} active registry entries`;
  renderCompetencyDetail();
}

function populateCompetencyFilters() {
  if (!elements.competencyDomainFilter || !elements.competencyKindFilter) return;
  const records = allSkillRecords();
  const currentGroup = elements.competencyGroupFilter.value;
  const currentDomain = elements.competencyDomainFilter.value;
  const currentKind = elements.competencyKindFilter.value;
  const groups = [...new Map(records.map((item) => [item.primary_group_id, item.group_name])).entries()]
    .sort((left, right) => String(left[1]).localeCompare(String(right[1])));
  const domains = [...new Set(records.map((item) => item.primary_domain).filter(Boolean))].sort();
  const kinds = [...new Set(records.map((item) => item.kind).filter(Boolean))].sort();
  elements.competencyGroupFilter.innerHTML = '<option value="">All groups</option>'
    + groups.map(([id, name]) => `<option value="${escapeHtml(id)}" translate="no">${escapeHtml(name)}</option>`).join("");
  elements.competencyDomainFilter.innerHTML = '<option value="">All domains</option>'
    + domains.map((value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`).join("");
  elements.competencyKindFilter.innerHTML = '<option value="">All kinds</option>'
    + kinds.map((value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`).join("");
  if (groups.some(([id]) => id === currentGroup)) elements.competencyGroupFilter.value = currentGroup;
  if (domains.includes(currentDomain)) elements.competencyDomainFilter.value = currentDomain;
  if (kinds.includes(currentKind)) elements.competencyKindFilter.value = currentKind;
}

async function loadCompetencyExplorer({ force = false } = {}) {
  if (competencyExplorerLoaded && !force) {
    renderCompetencyExplorer();
    return state.competencyExplorer;
  }
  elements.competencyMessage.className = "form-message";
  elements.competencyMessage.textContent = "Linking Career Vault evidence to the shared skills registry...";
  try {
    state.competencyExplorer = await api.getCompetencyExplorer();
    competencyExplorerLoaded = true;
    const items = allSkillRecords();
    if (!items.some((item) => item.id === state.selectedCompetencyId)) {
      state.selectedCompetencyId = items[0]?.id || null;
    }
    populateCompetencyFilters();
    renderCompetencyExplorer();
    elements.competencyMessage.textContent = `Evidence projection as of ${state.competencyExplorer.as_of_month}. Registry definitions remain editable in Groups.`;
    return state.competencyExplorer;
  } catch (error) {
    competencyExplorerLoaded = false;
    elements.competencyMessage.className = "form-message error";
    elements.competencyMessage.textContent = formatRequestError(error);
    throw error;
  }
}

function competencyProjectionMatchesRegistry(projection) {
  return Boolean(
    projection
    && state.skillsRegistry
    && projection.catalog_revision === state.skillsRegistry.catalog_revision
  );
}

async function loadHistoricalCompetencyExplorer({ force = false } = {}) {
  if (
    historicalCompetencyExplorerLoaded
    && !force
    && competencyProjectionMatchesRegistry(state.historicalCompetencyExplorer)
  ) {
    renderSkillsRegistryDetail();
    return state.historicalCompetencyExplorer;
  }
  if (
    historicalCompetencyExplorerLoaded
    && !competencyProjectionMatchesRegistry(state.historicalCompetencyExplorer)
  ) {
    historicalCompetencyExplorerLoaded = false;
    state.historicalCompetencyExplorer = null;
  }
  if (historicalCompetencyExplorerPromise && !force) {
    return historicalCompetencyExplorerPromise;
  }
  historicalCompetencyExplorerPromise = api.getCompetencyExplorer({ includeArchived: true })
    .then((projection) => {
      if (projection.include_archived_competencies !== true) {
        throw new Error("Historical evidence scope was not returned by the local API.");
      }
      if (!competencyProjectionMatchesRegistry(projection)) {
        throw new Error(
          "Skills Registry changed while historical evidence was loading. Refresh and try again."
        );
      }
      state.historicalCompetencyExplorer = projection;
      historicalCompetencyExplorerLoaded = true;
      renderSkillsRegistryDetail();
      return projection;
    })
    .catch((error) => {
      historicalCompetencyExplorerLoaded = false;
      state.historicalCompetencyExplorer = null;
      elements.skillsRegistryMessage.className = "form-message error";
      elements.skillsRegistryMessage.textContent = formatRequestError(error);
      return null;
    })
    .finally(() => {
      historicalCompetencyExplorerPromise = null;
    });
  return historicalCompetencyExplorerPromise;
}

function loadHistoricalEvidenceIfNeeded(skill) {
  if (skill?.archived === true) void loadHistoricalCompetencyExplorer();
}

async function loadSkillsHub({ force = false } = {}) {
  await Promise.all([
    loadSkillsRegistry(),
    loadCompetencyExplorer({ force: force || competencyExplorerLoaded }),
  ]);
  if (!competencyProjectionMatchesRegistry(state.competencyExplorer)) {
    await loadSkillsRegistry();
    await loadCompetencyExplorer({ force: true });
  }
  if (!competencyProjectionMatchesRegistry(state.competencyExplorer)) {
    throw new Error(
      "Skills Registry changed while evidence was loading. Refresh the view and try again."
    );
  }
  populateCompetencyFilters();
  renderSkillsRegistry();
  renderCompetencyExplorer();
}

function clearCompetencyFilters() {
  elements.competencySearch.value = "";
  elements.competencyGroupFilter.value = "";
  elements.competencyOriginFilter.value = "";
  elements.competencyEvidenceFilter.value = "";
  elements.competencyDomainFilter.value = "";
  elements.competencyKindFilter.value = "";
}

function openSkillEvidence(skillId) {
  if (!allSkillRecords().some((item) => item.id === skillId)) return;
  clearCompetencyFilters();
  state.selectedCompetencyId = skillId;
  setSkillsHubMode("all");
}

function manageRegistrySkill(skillId) {
  const skill = registrySkill(skillId);
  if (!skill) return;
  state.registryVisibility = "active";
  state.registrySkillVisibility = "active";
  state.selectedRegistryGroupId = skill.primary_group_id;
  state.selectedRegistrySkillId = skill.id;
  state.registryGroupEditing = false;
  state.registryCardDetailOpen = state.skillsRegistryLayout === "cards";
  setSkillsHubMode("groups");
}

function selectCompetency(competencyId) {
  if (!allSkillRecords().some((item) => item.id === competencyId)) return;
  state.selectedCompetencyId = competencyId;
  renderCompetencyExplorer();
  if (window.matchMedia("(max-width: 980px)").matches) {
    elements.competencyDetail.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

async function openCompetencyRecord(viewName, recordId) {
  if (!(await navigateToView(viewName))) return;
  try {
    if (viewName === "vault") {
      state.vaultVisibility = "active";
      elements.vaultSearch.value = "";
      elements.vaultCategoryFilter.value = "";
      await loadVaultExperiences();
      selectVaultExperience(recordId);
    } else if (viewName === "projects") {
      state.projectVisibility = "active";
      elements.projectSearch.value = "";
      elements.projectCategoryFilter.value = "";
      await loadVaultProjects();
      selectVaultProject(recordId);
    } else if (viewName === "education") {
      state.educationType = "certification";
      state.educationVisibility = "active";
      elements.educationSearch.value = "";
      await loadVaultEducation();
      selectEducationRecord(recordId);
    }
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

function demandGapStatusMeta(status) {
  return {
    supported: { label: "Supported", tone: "success" },
    thin_evidence: { label: "Thin evidence", tone: "warning" },
    evidence_not_found: { label: "Evidence not found", tone: "danger" },
  }[status] || { label: competencyLabel(status), tone: "info" };
}

function demandGapDateValue(dateValue) {
  const year = dateValue.getFullYear();
  const month = String(dateValue.getMonth() + 1).padStart(2, "0");
  const day = String(dateValue.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function demandGapPresetDates(preset) {
  if (!/^(30|90|180)$/.test(preset)) return { startDate: "", endDate: "" };
  const endDate = new Date();
  const startDate = new Date(endDate);
  startDate.setDate(startDate.getDate() - (Number(preset) - 1));
  return {
    startDate: demandGapDateValue(startDate),
    endDate: demandGapDateValue(endDate),
  };
}

function persistDemandGapsPeriodPreference() {
  try {
    window.localStorage.setItem(DEMAND_GAPS_PERIOD_KEY, JSON.stringify({
      preset: elements.demandGapsPeriodPreset.value,
      startDate: elements.demandGapsFrom.value,
      endDate: elements.demandGapsTo.value,
    }));
  } catch (_error) {
    // The period remains usable when browser storage is unavailable.
  }
}

function syncDemandGapsPeriodControls() {
  elements.demandGapsPeriodForm.hidden = elements.demandGapsPeriodPreset.value !== "custom";
}

function loadDemandGapsPeriodPreference() {
  let stored = {};
  try {
    stored = JSON.parse(window.localStorage.getItem(DEMAND_GAPS_PERIOD_KEY) || "{}");
  } catch (_error) {
    stored = {};
  }
  const allowed = new Set(["all", "30", "90", "180", "custom"]);
  const preset = allowed.has(stored.preset) ? stored.preset : "all";
  elements.demandGapsPeriodPreset.value = preset;
  if (preset === "custom") {
    elements.demandGapsFrom.value = /^\d{4}-\d{2}-\d{2}$/.test(stored.startDate || "") ? stored.startDate : "";
    elements.demandGapsTo.value = /^\d{4}-\d{2}-\d{2}$/.test(stored.endDate || "") ? stored.endDate : "";
  } else {
    const range = demandGapPresetDates(preset);
    elements.demandGapsFrom.value = range.startDate;
    elements.demandGapsTo.value = range.endDate;
  }
  syncDemandGapsPeriodControls();
}

function demandGapsPeriodText() {
  const preset = elements.demandGapsPeriodPreset.value;
  if (preset === "all") return "All saved jobs";
  if (preset === "30") return "Last 30 days";
  if (preset === "90") return "Last 90 days";
  if (preset === "180") return "Last 6 months";
  const startDate = elements.demandGapsFrom.value;
  const endDate = elements.demandGapsTo.value;
  if (startDate && endDate) return `${startDate} to ${endDate}`;
  if (startDate) return `From ${startDate}`;
  if (endDate) return `Through ${endDate}`;
  return "Custom period";
}

async function setDemandGapsPeriodPreset(preset, { reload = true } = {}) {
  if (!["all", "30", "90", "180", "custom"].includes(preset)) return;
  elements.demandGapsPeriodPreset.value = preset;
  if (preset === "custom") {
    syncDemandGapsPeriodControls();
    elements.demandGapsFilterMenu.open = true;
    elements.demandGapsFrom.focus();
    return;
  }
  const range = demandGapPresetDates(preset);
  elements.demandGapsFrom.value = range.startDate;
  elements.demandGapsTo.value = range.endDate;
  syncDemandGapsPeriodControls();
  persistDemandGapsPeriodPreference();
  if (reload) await loadDemandGaps({ force: true });
}

function requestedDemandGapRows() {
  return (state.demandGaps?.rows || []).filter(
    (item) => Number(item.distinct_job_count || 0) > 0
  );
}

function demandGapInsightRows() {
  const domain = elements.demandGapsDomain.value;
  return requestedDemandGapRows().filter(
    (item) => !domain || item.primary_domain === domain
  );
}

function sortDemandGapRows(rows) {
  const sort = elements.demandGapsSort.value;
  const number = (item, key) => Number(item[key] || 0);
  const name = (item) => String(item.competency_name || "");
  return [...rows].sort((left, right) => {
    if (sort === "name_asc") return name(left).localeCompare(name(right));
    if (sort === "required_desc") {
      return number(right, "required_job_count") - number(left, "required_job_count")
        || number(right, "preferred_job_count") - number(left, "preferred_job_count")
        || name(left).localeCompare(name(right));
    }
    if (sort === "preferred_desc") {
      return number(right, "preferred_job_count") - number(left, "preferred_job_count")
        || number(right, "required_job_count") - number(left, "required_job_count")
        || name(left).localeCompare(name(right));
    }
    const leftDemand = number(left, "required_job_count") + number(left, "preferred_job_count");
    const rightDemand = number(right, "required_job_count") + number(right, "preferred_job_count");
    return rightDemand - leftDemand
      || number(right, "required_job_count") - number(left, "required_job_count")
      || name(left).localeCompare(name(right));
  });
}

function filteredDemandGapRows() {
  const requirement = elements.demandGapsRequirement.value;
  const coverage = state.demandGapsCoverageFilter;
  const query = elements.demandGapsSearch.value.trim().toLocaleLowerCase();
  const rows = demandGapInsightRows().filter((item) => {
    if (coverage && item.status !== coverage) return false;
    if (requirement === "required" && !Number(item.required_job_count || 0)) return false;
    if (requirement === "preferred" && !Number(item.preferred_job_count || 0)) return false;
    if (query) {
      const haystack = [item.competency_name, item.primary_domain, item.kind]
        .map((value) => String(value || "").toLocaleLowerCase())
        .join(" ");
      if (!haystack.includes(query)) return false;
    }
    return true;
  });
  return sortDemandGapRows(rows);
}

function populateDemandGapFilters() {
  const currentDomain = elements.demandGapsDomain.value;
  const domains = state.demandGaps?.filters?.domains || [];
  elements.demandGapsDomain.innerHTML = '<option value="">All domains</option>'
    + domains.map(
      (value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`
    ).join("");
  if (domains.includes(currentDomain)) elements.demandGapsDomain.value = currentDomain;
}

function demandGapCounts(rows) {
  return {
    supported: rows.filter((item) => item.status === "supported").length,
    thin_evidence: rows.filter((item) => item.status === "thin_evidence").length,
    evidence_not_found: rows.filter((item) => item.status === "evidence_not_found").length,
  };
}

function demandGapDistinctJobCount(rows) {
  const jobIds = new Set();
  rows.forEach((item) => {
    (item.required_job_ids || []).forEach((jobId) => jobIds.add(jobId));
    (item.preferred_job_ids || []).forEach((jobId) => jobIds.add(jobId));
  });
  return jobIds.size;
}

function renderDemandGapCoverage(rows, counts) {
  const total = rows.length;
  const segments = [
    { status: "supported", label: "Supported", count: counts.supported },
    { status: "thin_evidence", label: "Thin evidence", count: counts.thin_evidence },
    { status: "evidence_not_found", label: "Evidence not found", count: counts.evidence_not_found },
  ];
  let offset = 0;
  const coverageRects = segments.filter((item) => item.count > 0).map((item) => {
    const rect = `<rect class="demand-coverage-rect demand-coverage-rect--${item.status}" x="${offset}" y="0" width="${item.count}" height="28"><title>${escapeHtml(item.label)}: ${item.count.toLocaleString()} of ${total.toLocaleString()}</title></rect>`;
    offset += item.count;
    return rect;
  }).join("");
  elements.demandGapsCoverageBar.innerHTML = total
    ? `<svg class="demand-coverage-chart" viewBox="0 0 ${total} 28" preserveAspectRatio="none" role="img" aria-label="Evidence coverage: ${counts.supported.toLocaleString()} supported, ${counts.thin_evidence.toLocaleString()} thin evidence, ${counts.evidence_not_found.toLocaleString()} evidence not found">${coverageRects}</svg>`
    : '<span class="demand-coverage-empty">No requested skills in this period</span>';
  elements.demandGapsCoverageLegend.innerHTML = segments.map((item) => `
    <button type="button" data-demand-gap-coverage="${item.status}" aria-pressed="${state.demandGapsCoverageFilter === item.status}"${item.count ? "" : " disabled"}>
      <span class="demand-coverage-key demand-coverage-key--${item.status}" aria-hidden="true"></span>
      <span>${escapeHtml(item.label)}</span>
      <strong>${item.count.toLocaleString()}</strong>
    </button>`).join("");
}

function renderDemandGapSummary() {
  const rows = demandGapInsightRows();
  const counts = demandGapCounts(rows);
  const source = state.demandGaps?.source || {};
  const jobCount = elements.demandGapsDomain.value
    ? demandGapDistinctJobCount(rows)
    : Number(source.distinct_job_count || 0);
  elements.demandGapsJobCount.textContent = jobCount.toLocaleString();
  elements.demandGapsSkillCount.textContent = rows.length.toLocaleString();
  elements.demandGapsAllCount.textContent = rows.length.toLocaleString();
  elements.demandGapsSupportedCount.textContent = counts.supported.toLocaleString();
  elements.demandGapsThinCount.textContent = counts.thin_evidence.toLocaleString();
  elements.demandGapsMissingCount.textContent = counts.evidence_not_found.toLocaleString();
  elements.demandGapsPeriodLabel.textContent = demandGapsPeriodText();
  elements.demandGapsFilterSummaryLabel.textContent = elements.demandGapsDomain.value ? "Filters (1)" : "Filters";
  renderDemandGapCoverage(rows, counts);
  elements.demandGapsWorkflow.querySelectorAll("[data-demand-gap-coverage]").forEach((button) => {
    const selected = button.dataset.demandGapCoverage === state.demandGapsCoverageFilter;
    button.setAttribute("aria-pressed", String(selected));
    button.classList.toggle("is-active", selected);
  });
}

function demandGapEvidenceCell(item) {
  const evidenceCount = Number(item.evidence_count || 0);
  const contextCount = Number(item.professional_context_count || 0);
  if (!evidenceCount) {
    return '<strong class="is-empty">Not found</strong><span>in Career Vault</span>';
  }
  const evidenceLabel = evidenceCount === 1 ? "record" : "records";
  const contextLabel = contextCount === 1 ? "professional context" : "professional contexts";
  return `<strong>${evidenceCount.toLocaleString()} ${evidenceLabel}</strong><span>${contextCount.toLocaleString()} ${contextLabel}</span>`;
}

function renderDemandGapInsights() {
  const rows = demandGapInsightRows();
  const ranked = [...rows].sort((left, right) => {
    const leftDemand = Number(left.required_job_count || 0) + Number(left.preferred_job_count || 0);
    const rightDemand = Number(right.required_job_count || 0) + Number(right.preferred_job_count || 0);
    return rightDemand - leftDemand
      || Number(right.required_job_count || 0) - Number(left.required_job_count || 0)
      || String(left.competency_name).localeCompare(String(right.competency_name));
  }).slice(0, 5);
  const maxDemand = Math.max(1, ...ranked.map(
    (item) => Number(item.required_job_count || 0) + Number(item.preferred_job_count || 0)
  ));
  elements.demandGapsTopDemand.innerHTML = ranked.length
    ? ranked.map((item) => {
      const required = Number(item.required_job_count || 0);
      const preferred = Number(item.preferred_job_count || 0);
      return `<button type="button" class="demand-ranking-item" data-demand-gap-id="${escapeHtml(item.competency_id)}">
        <span class="demand-ranking-copy"><strong>${escapeHtml(item.competency_name)}</strong><span>${required.toLocaleString()} required &middot; ${preferred.toLocaleString()} preferred</span></span>
        <progress class="demand-ranking-bar" max="${maxDemand}" value="${required + preferred}" aria-label="${required + preferred} demand occurrences"></progress>
      </button>`;
    }).join("")
    : '<p class="demand-insight-empty">No requested skills in this period.</p>';

  const priorities = [...rows].filter((item) => item.status === "evidence_not_found")
    .sort((left, right) => (
      Number(right.required_job_count || 0) - Number(left.required_job_count || 0)
      || Number(right.preferred_job_count || 0) - Number(left.preferred_job_count || 0)
      || String(left.competency_name).localeCompare(String(right.competency_name))
    )).slice(0, 5);
  elements.demandGapsPriority.innerHTML = priorities.length
    ? priorities.map((item, index) => `<button type="button" class="demand-priority-item" data-demand-gap-id="${escapeHtml(item.competency_id)}">
        <span class="demand-priority-rank">${index + 1}</span>
        <strong>${escapeHtml(item.competency_name)}</strong>
        <span>${Number(item.required_job_count || 0).toLocaleString()} required &middot; ${Number(item.preferred_job_count || 0).toLocaleString()} preferred</span>
      </button>`).join("")
    : '<p class="demand-insight-empty">No evidence gaps in this scope.</p>';
}

function demandGapSkillMeta(item) {
  const values = [];
  if (item.primary_domain && item.primary_domain !== "unclassified") {
    values.push(competencyLabel(item.primary_domain));
  }
  if (item.kind && item.kind !== "technology") values.push(competencyLabel(item.kind));
  return values;
}

function renderDemandGaps() {
  renderDemandGapSummary();
  renderDemandGapInsights();
  const allRequested = requestedDemandGapRows();
  const scopedRows = demandGapInsightRows();
  const rows = filteredDemandGapRows();
  if (!rows.length) {
    const periodApplied = Boolean(elements.demandGapsFrom.value || elements.demandGapsTo.value);
    const hasDemand = scopedRows.length > 0;
    const noPeriodDemand = !allRequested.length && periodApplied;
    const emptyTitle = noPeriodDemand
      ? "No saved-job demand in this period"
      : hasDemand
        ? "No rows match these filters"
        : elements.demandGapsDomain.value
          ? "No requested skills in this domain"
          : "No requested skills yet";
    const emptyDetail = noPeriodDemand
      ? "Choose All time to return to every saved job in the local ledger."
      : hasDemand
        ? "Try another search, demand level, or evidence status."
        : elements.demandGapsDomain.value
          ? "Choose another domain or clear the filter."
          : "Saved jobs with recognized required or preferred skills will appear here.";
    elements.demandGapsRows.innerHTML = `<div class="demand-gaps-empty">
      <strong>${emptyTitle}</strong>
      <span>${emptyDetail}</span>
      ${noPeriodDemand ? '<button type="button" class="secondary-button compact-button" data-demand-gaps-show-all>View all saved jobs</button>' : ""}
    </div>`;
  } else {
    elements.demandGapsRows.innerHTML = rows.map((item) => {
      const status = demandGapStatusMeta(item.status);
      const meta = demandGapSkillMeta(item);
      const selected = state.demandGapsDrawerOpen && state.selectedDemandGapId === item.competency_id;
      return `<div class="demand-gap-row-shell" role="listitem"><button type="button" class="demand-gap-row${selected ? " is-selected" : ""}" data-demand-gap-id="${escapeHtml(item.competency_id)}" aria-label="Open demand detail for ${escapeHtml(item.competency_name)}" aria-pressed="${selected}">
        <span class="demand-gap-skill" data-label="Skill"><strong>${escapeHtml(item.competency_name)}</strong>${meta.length ? `<span>${meta.map(escapeHtml).join(" &middot; ")}</span>` : ""}</span>
        <span class="demand-gap-demand" data-label="Demand"><strong>${Number(item.required_job_count || 0).toLocaleString()} required</strong><span>${Number(item.preferred_job_count || 0).toLocaleString()} preferred</span></span>
        <span class="demand-gap-evidence" data-label="Evidence">${demandGapEvidenceCell(item)}</span>
        <span class="demand-gap-coverage" data-label="Coverage"><span class="status-badge status-badge--${status.tone}" title="${escapeHtml(item.explanation)}">${escapeHtml(status.label)}</span></span>
        <span class="demand-gap-open" aria-hidden="true">${registryChevronIcon()}</span>
      </button></div>`;
    }).join("");
  }
  elements.demandGapsResultCount.textContent = rows.length === scopedRows.length
    ? `${scopedRows.length.toLocaleString()} requested skill${scopedRows.length === 1 ? "" : "s"}`
    : `${rows.length.toLocaleString()} of ${scopedRows.length.toLocaleString()} requested skills`;
  const source = state.demandGaps?.source || {};
  const duplicateCount = Number(source.duplicate_job_count || 0);
  const unregisteredCount = Number(source.unregistered_job_count || 0);
  const notes = [
    `${Number(source.distinct_job_count || 0).toLocaleString()} distinct of ${Number(source.saved_job_count || 0).toLocaleString()} saved jobs counted`,
  ];
  if (duplicateCount) notes.push(`${duplicateCount.toLocaleString()} exact duplicate${duplicateCount === 1 ? "" : "s"} excluded`);
  if (unregisteredCount) notes.push(`${unregisteredCount.toLocaleString()} normalized card${unregisteredCount === 1 ? "" : "s"} outside the ledger excluded`);
  elements.demandGapsSourceNote.textContent = `${notes.join(" / ")}.`;
  renderDemandGapDrawer();
}

async function loadDemandGaps({ force = false } = {}) {
  if (demandGapsLoaded && !force) {
    renderDemandGaps();
    return state.demandGaps;
  }
  const epoch = ++demandGapsLoadEpoch;
  elements.demandGapsMessage.className = "form-message";
  elements.demandGapsMessage.textContent = "Building a read-only comparison from saved jobs and Career Vault evidence...";
  elements.demandGapsRows.innerHTML = '<div class="demand-gaps-empty"><strong>Loading demand</strong><span>Counting distinct saved jobs and matching their registered skills.</span></div>';
  try {
    const payload = await api.getDemandGaps({
      startDate: elements.demandGapsFrom.value,
      endDate: elements.demandGapsTo.value,
    });
    if (epoch !== demandGapsLoadEpoch) return state.demandGaps;
    state.demandGaps = payload;
    demandGapsLoaded = true;
    populateDemandGapFilters();
    renderDemandGaps();
    elements.demandGapsMessage.textContent = `Read-only comparison as of ${payload.as_of_month}. Evidence not found is not a claim about candidate knowledge.`;
    return payload;
  } catch (error) {
    if (epoch !== demandGapsLoadEpoch) return state.demandGaps;
    demandGapsLoaded = false;
    elements.demandGapsMessage.className = "form-message error";
    elements.demandGapsMessage.textContent = formatRequestError(error);
    elements.demandGapsRows.innerHTML = '<div class="demand-gaps-empty"><strong>Demand could not be loaded</strong><span>Review the error below, then use the global refresh action to retry.</span></div>';
    throw error;
  }
}

async function applyDemandGapsPeriod() {
  const startDate = elements.demandGapsFrom.value;
  const endDate = elements.demandGapsTo.value;
  if (startDate && endDate && startDate > endDate) {
    elements.demandGapsMessage.className = "form-message error";
    elements.demandGapsMessage.textContent = "From date must be on or before To date.";
    elements.demandGapsFrom.focus();
    return;
  }
  elements.demandGapsPeriodPreset.value = "custom";
  syncDemandGapsPeriodControls();
  persistDemandGapsPeriodPreference();
  await loadDemandGaps({ force: true });
}

function setDemandGapCoverageFilter(value, { scroll = false } = {}) {
  if (!["", "supported", "thin_evidence", "evidence_not_found"].includes(value)) return;
  state.demandGapsCoverageFilter = value;
  renderDemandGaps();
  if (scroll) {
    document.querySelector(".demand-gaps-list-section")?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function relatedDemandGapJobs(item) {
  const demandByJob = new Map();
  const demandJobById = new Map(
    (state.demandGaps?.jobs || []).map((job) => [job.job_id, job])
  );
  (item?.required_job_ids || []).forEach((jobId) => demandByJob.set(jobId, new Set(["Required"])));
  (item?.preferred_job_ids || []).forEach((jobId) => {
    if (!demandByJob.has(jobId)) demandByJob.set(jobId, new Set());
    demandByJob.get(jobId).add("Preferred");
  });
  return [...demandByJob.entries()].map(([jobId, levels]) => {
    const demandJob = demandJobById.get(jobId);
    const loadedJob = state.jobs.find((candidate) => candidate.id === jobId);
    return {
      id: jobId,
      title: demandJob?.title || loadedJob?.title || jobId,
      company: demandJob?.company || loadedJob?.company || "Saved job",
      demand: [...levels].join(" + "),
    };
  });
}

function renderDemandGapDrawer() {
  if (!state.demandGapsDrawerOpen) return;
  const item = requestedDemandGapRows().find(
    (candidate) => candidate.competency_id === state.selectedDemandGapId
  );
  if (!item) {
    closeDemandGapDrawer({ restoreFocus: false });
    return;
  }
  const status = demandGapStatusMeta(item.status);
  const jobs = relatedDemandGapJobs(item);
  const visibleJobs = state.demandGapsRelatedJobsExpanded ? jobs : jobs.slice(0, 3);
  elements.demandGapDrawerTitle.textContent = item.competency_name;
  elements.demandGapDrawerStatus.innerHTML = `<span class="status-badge status-badge--${status.tone}">${escapeHtml(status.label)}</span><p>${escapeHtml(item.explanation)}</p>`;
  elements.demandGapDrawerRequired.textContent = Number(item.required_job_count || 0).toLocaleString();
  elements.demandGapDrawerPreferred.textContent = Number(item.preferred_job_count || 0).toLocaleString();
  elements.demandGapDrawerJobCount.textContent = `${jobs.length.toLocaleString()} job${jobs.length === 1 ? "" : "s"}`;
  elements.demandGapRelatedJobs.innerHTML = visibleJobs.length
    ? visibleJobs.map((job) => `<button type="button" class="demand-gap-related-job" data-demand-gap-job-id="${escapeHtml(job.id)}">
        <span><strong>${escapeHtml(job.title)}</strong><small>${escapeHtml(job.company)}</small></span>
        <em>${escapeHtml(job.demand)}</em>
      </button>`).join("")
    : '<p class="demand-insight-empty">No related saved jobs are available.</p>';
  elements.toggleDemandGapJobs.hidden = jobs.length <= 3;
  elements.toggleDemandGapJobs.textContent = state.demandGapsRelatedJobsExpanded ? "Show fewer jobs" : `View all ${jobs.length} related jobs`;
}

function openDemandGapDrawer(skillId) {
  const item = requestedDemandGapRows().find((candidate) => candidate.competency_id === skillId);
  if (!item) return;
  const activeElement = document.activeElement;
  demandGapDrawerReturnFocus = {
    element: activeElement,
    skillId,
    surface: ["demand-gap-row", "demand-ranking-item", "demand-priority-item"]
      .find((className) => activeElement?.classList?.contains(className)) || "",
  };
  state.selectedDemandGapId = skillId;
  state.demandGapsDrawerOpen = true;
  state.demandGapsRelatedJobsExpanded = false;
  elements.demandGapDrawer.classList.remove("hidden");
  elements.demandGapDrawerBackdrop.classList.remove("hidden");
  document.body.classList.add("demand-gap-drawer-open");
  renderDemandGaps();
  window.requestAnimationFrame(() => elements.closeDemandGapDrawer.focus());
}

function closeDemandGapDrawer({ restoreFocus = true } = {}) {
  const returnFocus = demandGapDrawerReturnFocus;
  state.demandGapsDrawerOpen = false;
  state.demandGapsRelatedJobsExpanded = false;
  state.selectedDemandGapId = null;
  elements.demandGapDrawer.classList.add("hidden");
  elements.demandGapDrawerBackdrop.classList.add("hidden");
  document.body.classList.remove("demand-gap-drawer-open");
  elements.demandGapsWorkflow.querySelectorAll(".is-selected").forEach((element) => {
    element.classList.remove("is-selected");
    element.setAttribute("aria-pressed", "false");
  });
  demandGapDrawerReturnFocus = null;
  if (!restoreFocus) return;
  let focusTarget = returnFocus?.element;
  if (!focusTarget?.isConnected && returnFocus?.skillId) {
    focusTarget = [...elements.demandGapsWorkflow.querySelectorAll("[data-demand-gap-id]")]
      .find((candidate) => (
        candidate.dataset.demandGapId === returnFocus.skillId
        && (!returnFocus.surface || candidate.classList.contains(returnFocus.surface))
      ));
  }
  if (focusTarget?.isConnected) focusTarget.focus();
}

async function openDemandGapSkill(skillId) {
  closeDemandGapDrawer({ restoreFocus: false });
  if (!(await navigateToView("skills"))) return;
  try {
    await loadSkillsHub();
    openSkillEvidence(skillId);
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

async function openSavedJobWorkspace(jobId, { focusMaterials = false } = {}) {
  if (!(await confirmReviewNavigation("job-library"))) return;
  state.selectedJobId = null;
  switchView("job-library");
  try {
    await loadJobs({ force: true, autoReanalyze: false });
    let pageCount = 0;
    while (!state.jobs.some((job) => job.id === jobId) && state.jobsNextCursor && pageCount < 20) {
      const previousCursor = state.jobsNextCursor;
      await loadMoreJobs();
      pageCount += 1;
      if (state.jobsNextCursor === previousCursor) break;
    }
    if (!state.jobs.some((job) => job.id === jobId)) {
      elements.runMessage.className = "form-message error";
      elements.runMessage.textContent = "That saved job is not available in the loaded ledger page.";
      return;
    }
    await selectJob(jobId, { scroll: true, autoReanalyze: false });
    if (
      focusMaterials
      && !elements.jobWorkspaceMaterials.classList.contains("hidden")
      && typeof elements.jobWorkspaceMaterials.scrollIntoView === "function"
    ) {
      elements.jobWorkspaceMaterials.scrollIntoView({ block: "start" });
    }
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

async function openDemandGapJob(jobId) {
  closeDemandGapDrawer({ restoreFocus: false });
  await openSavedJobWorkspace(jobId);
}

window.addEventListener("careerhub:open-saved-job", (event) => {
  const jobId = event.detail?.jobId;
  if (!jobId || /[\\/]/.test(jobId)) return;
  void openSavedJobWorkspace(jobId, {
    focusMaterials: event.detail?.focusMaterials === true,
  });
});

window.addEventListener("careerhub:discovery-selected", (event) => {
  const discoveryId = event.detail?.discoveryId;
  if (!discoveryId || /[\\/]/.test(discoveryId)) return;
  state.jobFeedDiscoveryId = discoveryId;
  state.jobFeedIntent = event.detail?.intent === "prepare" ? "prepare" : null;
  if (state.activeView === "job-feed") replaceLocalRouteForCurrentView();
});

function renderSkillQuickNames() {
  const creatingGroup = state.skillQuickMode === "group";
  const names = state.skillQuickNames || [];
  const multiple = names.length > 1;
  elements.skillQuickPills.hidden = creatingGroup || names.length === 0;
  elements.skillQuickPills.innerHTML = creatingGroup ? "" : names.map((name, index) => `
    <span class="review-skill-pill registry-skill-custom">
      <span>${escapeHtml(name)}</span>
      <button type="button" data-remove-skill-quick-name="${index}" aria-label="Remove ${escapeHtml(name)}">&times;</button>
    </span>`).join("");
  elements.skillQuickName.required = creatingGroup || names.length === 0;
  elements.skillQuickNameHelp.hidden = creatingGroup;
  elements.skillQuickAliasesField.hidden = creatingGroup || multiple;
  elements.skillQuickAliases.disabled = creatingGroup || multiple;
  if (multiple) elements.skillQuickAliases.value = "";
  elements.saveSkillQuickAction.textContent = creatingGroup
    ? "Add group"
    : names.length > 1
      ? `Add ${names.length} skills`
      : "Add skill";
}

function addSkillQuickNames(rawValue) {
  const values = String(rawValue || "")
    .split(/[,;\n]+/)
    .map((value) => value.trim().replace(/\s+/g, " "))
    .filter(Boolean);
  if (!values.length) return false;
  const tooLong = values.find((value) => value.length > 160);
  if (tooLong) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = `Skill labels must be 160 characters or fewer: ${tooLong.slice(0, 40)}…`;
    return false;
  }
  const names = [...(state.skillQuickNames || [])];
  const normalized = new Set(names.map(normalizeReviewSkillName));
  values.forEach((value) => {
    const key = normalizeReviewSkillName(value);
    if (!normalized.has(key)) {
      normalized.add(key);
      names.push(value);
    }
  });
  if (names.length > 100) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = "Add no more than 100 skills at once.";
    return false;
  }
  state.skillQuickNames = names;
  elements.skillQuickName.value = "";
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "";
  renderSkillQuickNames();
  return true;
}

function setSkillQuickMode(mode) {
  state.skillQuickMode = mode === "group" ? "group" : "skill";
  const creatingGroup = state.skillQuickMode === "group";
  elements.skillQuickTitle.textContent = creatingGroup
    ? "Add skill group"
    : state.skillQuickContext?.jobId
      ? "Add skill to this job"
      : "Add skill";
  elements.skillQuickNameLabel.textContent = creatingGroup ? "Group name" : "Skill name(s)";
  elements.skillQuickName.placeholder = creatingGroup ? "Office Automation" : "Type a skill, then comma or Enter";
  elements.skillQuickGroupField.hidden = creatingGroup;
  elements.skillQuickGroup.required = !creatingGroup;
  elements.skillQuickJobLevelField.classList.toggle("hidden", creatingGroup || !state.skillQuickContext?.jobId);
  renderSkillQuickNames();
  document.querySelectorAll("[data-skill-quick-mode]").forEach((button) => {
    button.classList.toggle("active", button.dataset.skillQuickMode === state.skillQuickMode);
  });
}

async function openSkillQuickAction(mode = "skill", context = null) {
  state.skillQuickContext = context;
  const attachingToJob = Boolean(context?.jobId);
  elements.skillQuickJobLevelField.classList.toggle("hidden", !attachingToJob);
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "Loading Skills Registry...";
  elements.skillQuickName.value = "";
  elements.skillQuickAliases.value = "";
  state.skillQuickNames = [];
  setSkillQuickMode(mode);
  if (attachingToJob) elements.skillQuickTitle.textContent = "Add skill to this job";
  if (!elements.skillQuickDialog.open) elements.skillQuickDialog.showModal();
  try {
    await loadSkillsRegistry({ keepMessage: true });
    populateSkillQuickGroups(context?.preferredGroupId || "");
    elements.skillQuickMessage.textContent = "";
    elements.skillQuickName.focus();
  } catch (error) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = formatRequestError(error);
  }
}

function closeSkillQuickAction() {
  state.skillQuickContext = null;
  elements.skillQuickJobLevelField.classList.add("hidden");
  if (elements.skillQuickDialog.open) elements.skillQuickDialog.close();
}

async function attachRegistrySkillsToJob(jobId, records, level) {
  const job = state.jobs.find((item) => item.id === jobId);
  if (!job?.valid) throw new Error("The selected job is no longer available.");
  const field = level === "required"
    ? "required_skills"
    : level === "preferred"
      ? "preferred_skills"
      : "mentioned_technologies";
  const existing = field === "mentioned_technologies"
    ? (job.digest?.mentioned_technologies || [])
    : (job[field] || []);
  const names = records.map((record) => record.name).filter(Boolean);
  const values = [...new Set([...existing, ...names])];
  await persistJobFacts(job, { [field]: values });
}

async function saveSkillQuickAction(event) {
  event.preventDefault();
  if (state.skillQuickSaving || !state.skillsRegistry) return;
  state.skillQuickSaving = true;
  elements.saveSkillQuickAction.disabled = true;
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "Validating Skills Registry...";
  try {
    let result;
    if (state.skillQuickMode === "group") {
      result = await api.createSkillGroup(
        state.skillsRegistry.revision,
        { name: elements.skillQuickName.value },
      );
      await loadSkillsRegistry({ keepMessage: true });
      const context = state.skillQuickContext;
      if (context?.reviewEntityId && Number.isInteger(context.pendingSkillIndex)) {
        const entity = currentReview()?.entities.find((item) => item.id === context.reviewEntityId);
        const pending = entity?.pending_skills?.[context.pendingSkillIndex];
        if (pending) {
          pending.primary_group_id = result.record.id;
          markReviewDirty();
          closeSkillQuickAction();
          renderCardEditor();
          return;
        }
      }
      setSkillQuickMode("skill");
      populateSkillQuickGroups(result.record.id);
      elements.skillQuickName.value = "";
      elements.skillQuickMessage.textContent = `Created ${result.record.name}. Add its first skill now.`;
      elements.skillQuickName.focus();
    } else {
      if (elements.skillQuickName.value.trim() && !addSkillQuickNames(elements.skillQuickName.value)) return;
      const names = [...state.skillQuickNames];
      if (!names.length) {
        elements.skillQuickMessage.className = "form-message error";
        elements.skillQuickMessage.textContent = "Add at least one skill name.";
        return;
      }
      const aliases = vaultLines(elements.skillQuickAliases.value, true);
      if (names.length === 1 && aliases.length) {
        const single = await api.createRegistrySkill(
          state.skillsRegistry.revision,
          {
            name: names[0],
            aliases,
            primary_group_id: elements.skillQuickGroup.value,
          },
        );
        result = { records: [single.record], created_skill_ids: [single.record.id], revision: single.revision };
      } else {
        result = await api.createRegistrySkills(
          state.skillsRegistry.revision,
          {
            names,
            primary_group_id: elements.skillQuickGroup.value,
          },
        );
      }
      await loadSkillsRegistry({ keepMessage: true });
      const selectedRecord = result.records.at(-1);
      const selectedSkill = selectedRecord ? registrySkill(selectedRecord.id) : null;
      state.selectedRegistryGroupId = selectedSkill?.group?.id || selectedRecord?.primary_group_id || elements.skillQuickGroup.value;
      state.selectedRegistrySkillId = selectedRecord?.id || null;
      renderSkillsRegistry();
      const context = state.skillQuickContext;
      if (context?.jobId) {
        await attachRegistrySkillsToJob(
          context.jobId,
          result.records,
          elements.skillQuickJobLevel.value,
        );
        const count = result.records.length;
        closeSkillQuickAction();
        toast.show({ message: `${count} skill${count === 1 ? "" : "s"} attached to this job.` });
        return;
      }
      const createdCount = result.created_skill_ids?.length || 0;
      const existingCount = result.records.length - createdCount;
      elements.skillQuickMessage.textContent = createdCount
        ? `Added ${createdCount} skill${createdCount === 1 ? "" : "s"}.${existingCount ? ` ${existingCount} already matched existing skills.` : ""} They are ready for resume and job matching.`
        : "All entered names already matched existing canonical skills.";
      if (state.activeView === "job-library") {
        const selected = state.jobs.find((job) => job.id === state.selectedJobId);
        if (selected?.status === "draft") await reanalyzeSelectedJob();
      }
      state.skillQuickNames = [];
      elements.skillQuickName.value = "";
      elements.skillQuickAliases.value = "";
      renderSkillQuickNames();
      elements.skillQuickName.focus();
    }
  } catch (error) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "SKILL_REGISTRY_REVISION_CONFLICT") {
      await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
    }
  } finally {
    state.skillQuickSaving = false;
    elements.saveSkillQuickAction.disabled = false;
  }
}

function setProfileWorkAuthorization(value) {
  const authorization = String(value || "").trim();
  const knownOption = [...elements.profileWorkAuthorization.options]
    .some((option) => option.value === authorization && authorization !== "Other");
  elements.profileWorkAuthorization.value = knownOption ? authorization : (authorization ? "Other" : "");
  elements.profileWorkAuthorizationOther.value = knownOption ? "" : authorization;
  syncProfileWorkAuthorizationOtherField();
}

function syncProfileWorkAuthorizationOtherField() {
  const isOther = elements.profileWorkAuthorization.value === "Other";
  elements.profileWorkAuthorizationOtherField.hidden = !isOther;
  elements.profileWorkAuthorizationOther.disabled = !isOther || !state.profileEditing;
  elements.profileWorkAuthorizationOther.required = isOther;
}

function profileWorkAuthorizationValue() {
  return elements.profileWorkAuthorization.value === "Other"
    ? elements.profileWorkAuthorizationOther.value.trim()
    : elements.profileWorkAuthorization.value;
}

function renderProfileRoleTargets(record = state.profileRecord) {
  const roles = record?.role_targets || vaultLines(elements.profileRoleTargets.value, true);
  elements.profileRoleTargetTags.innerHTML = roles.length
    ? roles.map((role) => `<span class="tag" translate="no">${escapeHtml(role)}</span>`).join("")
    : '<span class="muted-text">No target roles recorded.</span>';
}

function setProfileEditing(editing) {
  state.profileEditing = Boolean(editing);
  elements.profileEditorShell.classList.toggle("is-readonly", !state.profileEditing);
  elements.editProfileButton.disabled = state.profileEditing;
  elements.editProfileButton.setAttribute("aria-pressed", String(state.profileEditing));
  elements.editProfileButton.textContent = state.profileEditing ? "Editing" : "Edit profile";
  elements.profileForm.querySelectorAll("input, select, textarea").forEach((control) => {
    if (control.closest(".eligibility-profile-section, .profile-policy-card")) return;
    control.disabled = !state.profileEditing;
  });
  elements.cancelProfileButton.hidden = !state.profileEditing;
  elements.saveProfileButton.hidden = !state.profileEditing;
  syncProfileWorkAuthorizationOtherField();
  renderEligibilityProfile();
  renderHardBlockerPolicy();
  renderProfileRoleTargets();
}

function populateProfileForm(record) {
  const contact = record?.contact || {};
  const allowsUnknownFacts = record?.vault_schema_version === 2;
  [elements.profileMajor, elements.profileTargetLocation, elements.profileWorkAuthorization]
    .forEach((control) => { control.required = !allowsUnknownFacts; });
  elements.profileEditorTitle.textContent = record?.name || "Candidate profile";
  elements.profileName.value = record?.name || "";
  elements.profileGivenName.value = record?.given_name || "";
  elements.profileFamilyName.value = record?.family_name || "";
  elements.profilePreferredName.value = record?.preferred_name || "";
  elements.profileMajor.value = record?.major || "";
  elements.profileCurrentLocation.value = record?.current_location || "";
  elements.profileTargetLocation.value = record?.target_location || "";
  elements.profileLocationFlexibility.value = record?.location_flexibility || "";
  setProfileWorkAuthorization(record?.work_authorization);
  elements.profileEmail.value = contact.email || "";
  elements.profilePhone.value = contact.phone || "";
  elements.profileAddress.value = contact.address || "";
  elements.profileAddressLine1.value = contact.address_line1 || "";
  elements.profileAddressLine2.value = contact.address_line2 || "";
  elements.profileCity.value = contact.city || "";
  elements.profileRegion.value = contact.region || "";
  elements.profilePostalCode.value = contact.postal_code || "";
  elements.profileCountry.value = contact.country || "";
  elements.profileLinkedin.value = contact.linkedin || "";
  elements.profileGithub.value = contact.github || "";
  elements.profileWebsite.value = contact.website || "";
  elements.profileRoleTargets.value = (record?.role_targets || []).join("\n");
  state.profileFormDirty = false;
  renderProfileRoleTargets(record);
}

const vaultSetupImport = { initialized: false, busy: false, request: null, plan: null };
const vaultSetupNames = ["profile.yaml", "education.yaml", "visa_profile.yaml", "experience_blocks.yaml", "project_blocks.yaml", "skill_blocks.yaml", "proof_blocks.yaml", "style_rules.yaml"];

async function ensureVaultSetup() {
  if (api.capabilities.vault_setup_import !== true) return true;
  const panel = document.querySelector("#vault-setup-panel");
  const input = document.querySelector("#vault-setup-files");
  const preview = document.querySelector("#vault-setup-preview");
  const apply = document.querySelector("#vault-setup-apply");
  const message = document.querySelector("#vault-setup-message");
  const name = document.querySelector("#vault-setup-name");
  const create = document.querySelector("#vault-setup-create");
  document.querySelector("#vault-setup-new").hidden = api.capabilities.vault_setup_empty !== true;
  const controls = () => {
    input.disabled = vaultSetupImport.busy;
    name.disabled = vaultSetupImport.busy;
    create.disabled = vaultSetupImport.busy || !name.value.trim() || api.capabilities.vault_setup_empty !== true;
    preview.disabled = vaultSetupImport.busy || input.files.length === 0;
    apply.disabled = vaultSetupImport.busy || !vaultSetupImport.plan;
    panel.setAttribute("aria-busy", String(vaultSetupImport.busy));
  };
  if (!vaultSetupImport.initialized) {
    vaultSetupImport.initialized = true;
    input.addEventListener("change", () => {
      vaultSetupImport.request = null; vaultSetupImport.plan = null;
      message.textContent = "Check all eight files before confirming their import.";
      controls();
    });
    name.addEventListener("input", () => {
      vaultSetupImport.request = null; vaultSetupImport.plan = null;
      message.textContent = "Check the new profile before confirming setup.";
      controls();
    });
    const operation = async (isApply, newProfile = false) => {
      if (vaultSetupImport.busy || isApply && !vaultSetupImport.plan) return;
      vaultSetupImport.busy = true; controls();
      message.innerHTML = renderLoadingState({ label: isApply ? "Setting up Career Vault…" : "Checking setup…", compact: true });
      try {
        if (!isApply) {
          vaultSetupImport.plan = null; vaultSetupImport.request = null;
          let request;
          if (newProfile) {
            if (api.capabilities.vault_setup_empty !== true || !name.value.trim()) throw new Error("Enter your name to create a profile.");
            request = { contract_version: 2, profile_name: name.value };
          } else {
            const selected = Array.from(input.files);
            if (selected.length !== 8 || new Set(selected.map((file) => file.name)).size !== 8 || selected.some((file) => !vaultSetupNames.includes(file.name) || file.size > 2 * 1024 * 1024)) {
              throw new Error("Select each of the eight listed YAML files once, at most 2 MB per file.");
            }
            const files = {};
            for (const file of selected) {
              files[file.name] = await new Promise((resolve, reject) => {
                const reader = new FileReader();
                reader.onload = () => resolve(String(reader.result).split(",", 2)[1]);
                reader.onerror = () => reject(new Error("A selected file could not be read."));
                reader.onabort = () => reject(new Error("Reading the selected file was canceled."));
                reader.readAsDataURL(file);
              });
            }
            request = { contract_version: 1, files };
          }
          const plan = await api.json("/api/vault/setup/preview", "POST", request, { signal: AbortSignal.timeout(65000) });
          if (!/^sha256:[a-f0-9]{64}$/.test(plan?.plan_id) || !Array.isArray(plan.create) || !Array.isArray(plan.retain)) throw new Error("The import preview could not be verified.");
          vaultSetupImport.request = request; vaultSetupImport.plan = plan;
          apply.textContent = newProfile ? "Create profile" : "Confirm import";
          message.textContent = `${plan.create.length} files to create; ${plan.retain.length} identical files retained. No existing file will be replaced. Confirm to continue.`;
        } else {
          const createdNew = vaultSetupImport.request.contract_version === 2;
          await api.json("/api/vault/setup/import", "POST", { ...vaultSetupImport.request, confirmed_plan: vaultSetupImport.plan.plan_id }, { signal: AbortSignal.timeout(65000) });
          vaultSetupImport.request = null; vaultSetupImport.plan = null; input.value = "";
          await ensureViewData("profile", { force: true });
          elements.profileMessage.textContent = createdNew ? "Profile created. Add your details when you are ready; unknown facts stay unconfirmed." : "Career Vault imported. Review your profile before using it.";
        }
      } catch (error) {
        vaultSetupImport.plan = null; vaultSetupImport.request = null;
        const code = error?.payload?.error_code || "";
        const hint = code === "VAULT_SETUP_FILE_CONFLICT"
          ? "This setup differs from data already in this Home. Use the original profile name or matching import files; existing files cannot be overwritten."
          : error?.status === 422
            ? "The selected files do not form a valid Career Vault. Check their YAML fields and cross-file references."
            : code || error.message;
        message.textContent = isApply
          ? `Setup was not confirmed complete. Some files may already be saved; check the same name or files again before retrying. ${code}`
          : `Setup check failed. Existing files were not replaced. ${hint}`;
      } finally { vaultSetupImport.busy = false; controls(); }
    };
    preview.addEventListener("click", () => operation(false));
    create.addEventListener("click", () => operation(false, true));
    apply.addEventListener("click", () => operation(true));
  }
  // Never flash an editable empty profile while its storage state is unknown.
  elements.profileEditorShell.hidden = true;
  elements.editProfileButton.hidden = true;
  panel.hidden = false;
  try {
    const setup = await api.request("/api/vault/setup", { signal: AbortSignal.timeout(15000) });
    if (setup.state === "configured") {
      panel.hidden = true; elements.profileEditorShell.hidden = false; elements.editProfileButton.hidden = false;
      return true;
    }
    if (!["not_configured", "incomplete"].includes(setup.state)) throw new Error("Unexpected Vault setup state.");
    if (!vaultSetupImport.busy) message.textContent = setup.state === "incomplete"
      ? "Some Vault files already exist. Repeat setup with the same name or matching import files to finish. Nothing is overwritten."
      : "This Data Home has no candidate profile yet.";
  } catch (error) { message.textContent = `Existing Career Vault files could not be validated. Nothing was reset. ${error?.payload?.error_code || "Check the local connection and try again."}`; }
  controls();
  return false;
}

async function loadVaultProfile({ keepMessage = false, refresh = false } = {}) {
  const payload = (await getVaultSnapshot({ refresh })).profile;
  state.profileRecord = payload.record || {};
  state.profileRevision = payload.revision;
  populateProfileForm(state.profileRecord);
  setProfileEditing(false);
  if (!keepMessage) elements.profileMessage.textContent = "";
}

function optionalBooleanControlValue(control) {
  if (control.value === "") return null;
  return control.value === "true";
}

function setOptionalBooleanControl(control, value) {
  control.value = typeof value === "boolean" ? String(value) : "";
}

function populateEligibilityProfile(snapshot) {
  const record = snapshot?.record || {};
  const allowsUnknownFacts = snapshot?.vault_schema_version === 2;
  [elements.eligibilitySponsorshipFuture, elements.eligibilityStemOpt].forEach((control) => {
    control.querySelector('option[value=""]').disabled = !allowsUnknownFacts;
  });
  elements.eligibilityCurrentStatus.value = record.current_status || "";
  elements.eligibilityWorkAuthorization.value = record.work_authorization || "";
  setOptionalBooleanControl(
    elements.eligibilitySponsorshipNow,
    record.requires_sponsorship_now,
  );
  setOptionalBooleanControl(
    elements.eligibilitySponsorshipFuture,
    record.requires_future_sponsorship,
  );
  setOptionalBooleanControl(
    elements.eligibilityCitizenshipConflict,
    record.citizenship_required_blocker,
  );
  setOptionalBooleanControl(
    elements.eligibilityClearanceConflict,
    record.clearance_required_blocker,
  );
  setOptionalBooleanControl(elements.eligibilityStemOpt, allowsUnknownFacts ? record.stem_opt_eligible : record.stem_opt_eligible === true);
  state.eligibilityProfileDirty = false;
}

function renderEligibilityProfile() {
  const busy = state.eligibilityProfileLoading || state.eligibilityProfileSaving || !state.profileEditing;
  // Empty read-only facts must not look like the example visa in an input hint.
  elements.eligibilityCurrentStatus.placeholder = state.profileEditing ? "For example, F-1 OPT" : "Not confirmed";
  elements.eligibilityWorkAuthorization.placeholder = state.profileEditing ? "Authorized to work in the United States under…" : "Not confirmed";
  [
    elements.eligibilityCurrentStatus,
    elements.eligibilityWorkAuthorization,
    elements.eligibilitySponsorshipNow,
    elements.eligibilitySponsorshipFuture,
    elements.eligibilityCitizenshipConflict,
    elements.eligibilityClearanceConflict,
    elements.eligibilityStemOpt,
  ].forEach((input) => { input.disabled = busy; });
  elements.saveEligibilityProfileButton.disabled = busy || !state.eligibilityProfileDirty;
  elements.saveEligibilityProfileButton.textContent = state.eligibilityProfileSaving
    ? "Saving…"
    : "Save eligibility facts";
}

async function loadEligibilityProfile({ keepMessage = false } = {}) {
  state.eligibilityProfileLoading = true;
  renderEligibilityProfile();
  try {
    state.eligibilityProfile = await api.getVaultEditor("eligibility");
    populateEligibilityProfile(state.eligibilityProfile);
    if (!keepMessage) {
      elements.eligibilityProfileMessage.className = "form-message";
      elements.eligibilityProfileMessage.textContent = "";
    }
  } catch (error) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = formatRequestError(error);
  } finally {
    state.eligibilityProfileLoading = false;
    renderEligibilityProfile();
  }
}

function markEligibilityProfileDirty() {
  if (state.eligibilityProfileLoading || state.eligibilityProfileSaving) return;
  state.eligibilityProfileDirty = true;
  elements.eligibilityProfileMessage.className = "form-message";
  elements.eligibilityProfileMessage.textContent = "Unsaved eligibility change.";
  renderEligibilityProfile();
}

async function saveEligibilityProfile() {
  if (
    !state.eligibilityProfile
    || !state.eligibilityProfileDirty
    || state.eligibilityProfileSaving
  ) return;
  if (state.eligibilityProfile.vault_schema_version !== 2 && (
    !elements.eligibilityCurrentStatus.value.trim()
    || !elements.eligibilityWorkAuthorization.value.trim()
    || elements.eligibilitySponsorshipFuture.value === ""
    || elements.eligibilityStemOpt.value === ""
  )) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = "This legacy profile requires status, authorization, future sponsorship and STEM OPT facts.";
    return;
  }
  state.eligibilityProfileSaving = true;
  elements.eligibilityProfileMessage.className = "form-message";
  elements.eligibilityProfileMessage.textContent = "Saving confirmed eligibility facts…";
  renderEligibilityProfile();
  try {
    state.eligibilityProfile = await api.updateEligibilityProfile(
      {
        current_status: elements.eligibilityCurrentStatus.value,
        work_authorization: elements.eligibilityWorkAuthorization.value,
        requires_sponsorship_now: optionalBooleanControlValue(elements.eligibilitySponsorshipNow),
        requires_future_sponsorship: optionalBooleanControlValue(elements.eligibilitySponsorshipFuture),
        stem_opt_eligible: optionalBooleanControlValue(elements.eligibilityStemOpt),
        citizenship_required_blocker: optionalBooleanControlValue(elements.eligibilityCitizenshipConflict),
        clearance_required_blocker: optionalBooleanControlValue(elements.eligibilityClearanceConflict),
      },
      state.eligibilityProfile.revision,
    );
    populateEligibilityProfile(state.eligibilityProfile);
    elements.eligibilityProfileMessage.className = "form-message success";
    elements.eligibilityProfileMessage.textContent = "Eligibility facts saved. Existing assessments refresh when opened.";
  } catch (error) {
    elements.eligibilityProfileMessage.className = "form-message error";
    elements.eligibilityProfileMessage.textContent = formatRequestError(error);
  } finally {
    state.eligibilityProfileSaving = false;
    renderEligibilityProfile();
  }
}

function populateHardBlockerPolicy(snapshot) {
  const classes = snapshot?.classes || {};
  elements.hardBlockerWorkAuthorization.checked = classes.citizenship_work_authorization !== false;
  elements.hardBlockerClearance.checked = classes.active_clearance !== false;
  elements.hardBlockerExperience.checked = classes.minimum_experience_years !== false;
  state.hardBlockerPolicyDirty = false;
}

function renderHardBlockerPolicy() {
  const busy = state.hardBlockerPolicyLoading || state.hardBlockerPolicySaving || !state.profileEditing;
  [
    elements.hardBlockerWorkAuthorization,
    elements.hardBlockerClearance,
    elements.hardBlockerExperience,
  ].forEach((input) => { input.disabled = busy; });
  elements.saveHardBlockerPolicyButton.disabled = busy || !state.hardBlockerPolicyDirty;
  elements.saveHardBlockerPolicyButton.textContent = state.hardBlockerPolicySaving
    ? "Saving…"
    : "Save policy";
}

async function loadHardBlockerPolicy({ keepMessage = false } = {}) {
  state.hardBlockerPolicyLoading = true;
  renderHardBlockerPolicy();
  try {
    state.hardBlockerPolicy = await api.getHardBlockerPolicy();
    populateHardBlockerPolicy(state.hardBlockerPolicy);
    if (!keepMessage) {
      elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
      elements.hardBlockerPolicyMessage.textContent = "";
    }
  } catch (error) {
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message error";
    elements.hardBlockerPolicyMessage.textContent = formatRequestError(error);
  } finally {
    state.hardBlockerPolicyLoading = false;
    renderHardBlockerPolicy();
  }
}

function markHardBlockerPolicyDirty() {
  if (state.hardBlockerPolicyLoading || state.hardBlockerPolicySaving) return;
  state.hardBlockerPolicyDirty = true;
  elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
  elements.hardBlockerPolicyMessage.textContent = "Unsaved policy change.";
  renderHardBlockerPolicy();
}

async function saveHardBlockerPolicy() {
  if (
    !state.hardBlockerPolicy
    || !state.hardBlockerPolicyDirty
    || state.hardBlockerPolicySaving
  ) return;
  state.hardBlockerPolicySaving = true;
  elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message";
  elements.hardBlockerPolicyMessage.textContent = "Saving local assessment policy…";
  renderHardBlockerPolicy();
  try {
    state.hardBlockerPolicy = await api.updateHardBlockerPolicy(
      {
        citizenship_work_authorization: elements.hardBlockerWorkAuthorization.checked,
        active_clearance: elements.hardBlockerClearance.checked,
        minimum_experience_years: elements.hardBlockerExperience.checked,
      },
      state.hardBlockerPolicy.revision,
    );
    populateHardBlockerPolicy(state.hardBlockerPolicy);
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message success";
    elements.hardBlockerPolicyMessage.textContent = "Policy saved. Existing assessments refresh when opened.";
  } catch (error) {
    elements.hardBlockerPolicyMessage.className = "form-message profile-policy-message error";
    elements.hardBlockerPolicyMessage.textContent = formatRequestError(error);
  } finally {
    state.hardBlockerPolicySaving = false;
    renderHardBlockerPolicy();
  }
}

async function cancelProfileEdit() {
  if (!state.profileFormDirty) {
    setProfileEditing(false);
    elements.profileMessage.textContent = "";
    return;
  }
  if (!window.confirm("Discard unsaved profile changes?")) return;
  try {
    await loadVaultProfile({ keepMessage: true, refresh: true });
    elements.profileMessage.className = "form-message";
    elements.profileMessage.textContent = "Unsaved changes discarded.";
  } catch (error) {
    elements.profileMessage.className = "form-message error";
    elements.profileMessage.textContent = formatRequestError(error);
  }
}

async function saveVaultProfile(event) {
  event.preventDefault();
  const record = {
    name: elements.profileName.value,
    given_name: elements.profileGivenName.value,
    family_name: elements.profileFamilyName.value,
    preferred_name: elements.profilePreferredName.value,
    major: elements.profileMajor.value,
    current_location: elements.profileCurrentLocation.value,
    target_location: elements.profileTargetLocation.value,
    location_flexibility: elements.profileLocationFlexibility.value,
    work_authorization: profileWorkAuthorizationValue(),
    email: elements.profileEmail.value,
    phone: elements.profilePhone.value,
    address: elements.profileAddress.value,
    address_line1: elements.profileAddressLine1.value,
    address_line2: elements.profileAddressLine2.value,
    city: elements.profileCity.value,
    region: elements.profileRegion.value,
    postal_code: elements.profilePostalCode.value,
    country: elements.profileCountry.value,
    linkedin: elements.profileLinkedin.value,
    github: elements.profileGithub.value,
    website: elements.profileWebsite.value,
    role_targets: vaultLines(elements.profileRoleTargets.value, true),
  };
  elements.saveProfileButton.disabled = true;
  elements.saveProfileButton.textContent = "Saving...";
  elements.profileMessage.className = "form-message";
  elements.profileMessage.textContent = "Validating canonical Career Vault...";
  try {
    const result = await requestJson("/api/vault/profile", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expected_revision: state.profileRevision, record }),
    });
    state.profileRecord = result.record;
    state.profileRevision = result.revision;
    populateProfileForm(result.record);
    setProfileEditing(false);
    elements.profileMessage.textContent = "Profile saved.";
  } catch (error) {
    elements.profileMessage.className = "form-message error";
    elements.profileMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveProfileButton.disabled = false;
    elements.saveProfileButton.textContent = "Save profile";
    await pollLogs();
  }
}
function educationRecords() {
  return state.educationType === "school" ? state.vaultSchools : state.vaultCertifications;
}

function selectedEducationRecord() {
  return educationRecords().find((record) => record.id === state.selectedEducationId) || null;
}

function statusEducationRecords() {
  const archived = state.educationVisibility === "archived";
  return educationRecords().filter((record) => (record.archived === true) === archived);
}

function filteredEducationRecords() {
  const query = elements.educationSearch.value.trim().toLocaleLowerCase();
  return statusEducationRecords().filter((record) => {
    if (!query) return true;
    const values = state.educationType === "school"
      ? [record.institution, record.degree, record.major, record.location, ...(record.honors || [])]
      : [record.name, record.credential, record.completed, ...(record.proof_points || [])];
    return values.join(" ").toLocaleLowerCase().includes(query);
  });
}

function educationTypeLabel(plural = false) {
  if (state.educationType === "school") return plural ? "Schools" : "school";
  return plural ? "Certifications" : "certification";
}

function renderEducationList() {
  const records = filteredEducationRecords();
  const statusRecords = statusEducationRecords();
  const current = educationRecords();
  const activeCount = current.filter((record) => record.archived !== true).length;
  const archivedCount = current.length - activeCount;
  elements.educationSchoolCount.textContent = state.vaultSchools.length;
  elements.educationCertificationCount.textContent = state.vaultCertifications.length;
  elements.educationArchivedCount.textContent = archivedCount;
  elements.educationSchoolFilter.classList.toggle("active", state.educationType === "school");
  elements.educationCertificationFilter.classList.toggle("active", state.educationType === "certification");
  elements.educationSchoolFilter.setAttribute("aria-pressed", state.educationType === "school");
  elements.educationCertificationFilter.setAttribute("aria-pressed", state.educationType === "certification");
  updateArchiveScopeToggle(
    elements.educationArchivedFilter,
    state.educationVisibility === "archived",
    "education records",
  );
  elements.educationListTitle.textContent = educationTypeLabel(true);
  elements.newEducationButton.textContent = `New ${educationTypeLabel()}`;
  elements.educationRecordCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.educationRecordList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedEducationId;
      const primary = state.educationType === "school" ? record.institution : record.name;
      const secondary = state.educationType === "school"
        ? [record.degree, record.major].filter(Boolean).join(" / ")
        : record.credential;
      const timing = state.educationType === "school"
        ? (record.dates || record.graduation_date || "Dates not set")
        : (record.completed || "Completion not set");
      const itemCount = state.educationType === "school"
        ? (record.honors || []).length
        : (record.proof_points || []).length;
      return `<button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-education-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open this canonical ${educationTypeLabel()} in the editor.">
          <span class="vault-card-kind vault-card-only">${escapeHtml(educationTypeLabel())}</span>
          <span class="vault-record-main"><strong>${escapeHtml(primary || record.id)}</strong></span>
          <span class="vault-keyword-preview">${escapeHtml(secondary || "No additional detail")}</span>
          <span class="vault-record-meta"><span>${escapeHtml(timing)}</span><span>${itemCount} items</span></span>
          ${renderVaultCardPreview(
            state.educationType === "school" ? record.honors : record.proof_points,
            state.educationType === "school" ? "No honors listed" : "No evidence items listed",
          )}
          <span class="vault-record-counts vault-education-record-id">${escapeHtml(record.id)}${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : `<div class="empty-state">No ${state.educationVisibility} ${educationTypeLabel(true).toLocaleLowerCase()} match this search.</div>`;
}

function setEducationFieldAvailability() {
  const school = state.educationType === "school";
  elements.schoolFields.hidden = !school;
  elements.certificationFields.hidden = school;
  elements.schoolFields.querySelectorAll("input, textarea").forEach((field) => { field.disabled = !school; });
  elements.certificationFields.querySelectorAll("input, textarea").forEach((field) => { field.disabled = school; });
}

function populateEducationForm(record) {
  const isNew = state.educationEditorMode === "new";
  setEducationFieldAvailability();
  elements.educationEditorSource.textContent = isNew
    ? "New manual record"
    : (record?.source === "manual" ? "Manual record" : "Canonical record");
  const primary = state.educationType === "school" ? record?.institution : record?.name;
  elements.educationEditorTitle.textContent = isNew
    ? `New ${educationTypeLabel()}`
    : (primary || "Education details");
  elements.educationEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.educationInstitution.value = record?.institution || "";
  elements.educationDegree.value = record?.degree || "";
  elements.educationMajor.value = record?.major || "";
  elements.educationLocation.value = record?.location || "";
  educationDateRange.setValue(record || {});
  graduationMonth.setValue(record?.graduation_date || "", record?.graduation_month || "");
  elements.educationGpa.value = record?.gpa || "";
  elements.educationStatus.value = record?.status || "";
  elements.educationHonors.value = (record?.honors || []).join("\n");
  elements.certificationName.value = record?.name || "";
  elements.certificationCredential.value = record?.credential || "";
  certificationCompletedMonth.setValue(record?.completed || "", record?.completed_month || "");
  elements.certificationProofPoints.value = (record?.proof_points || []).join("\n");
  elements.archiveEducationButton.hidden = isNew || !record;
  if (!isNew && record) {
    const restoring = record.archived === true;
    elements.archiveEducationButton.textContent = restoring ? "Restore" : "Archive";
    elements.archiveEducationButton.dataset.tooltip = restoring
      ? `Return this ${educationTypeLabel()} to active workflows.`
      : `Move this ${educationTypeLabel()} out of active workflows while preserving it.`;
  }
  elements.saveEducationButton.textContent = `Save ${educationTypeLabel()}`;
  state.educationFormDirty = false;
}

function renderEducation() {
  renderEducationList();
  if (state.educationEditorMode === "new") return;
  let record = selectedEducationRecord();
  const visibleRecords = statusEducationRecords();
  if (!record || (record.archived === true) !== (state.educationVisibility === "archived")) {
    state.selectedEducationId = visibleRecords[0]?.id || null;
    record = selectedEducationRecord();
    renderEducationList();
  }
  populateEducationForm(record);
}

async function loadVaultEducation({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("education", async () => {
    const payload = (await getVaultSnapshot({ refresh })).education;
    state.vaultSchools = payload.schools || [];
    state.vaultCertifications = payload.certifications || [];
    state.educationRevision = payload.revision;
    const visibleRecords = statusEducationRecords();
    if (!visibleRecords.some((record) => record.id === state.selectedEducationId)) {
      state.selectedEducationId = visibleRecords[0]?.id || null;
    }
    state.educationEditorMode = state.selectedEducationId ? "edit" : "new";
    renderEducation();
    if (!keepMessage) elements.educationMessage.textContent = "";
  });
}

function setEducationType(recordType) {
  if (recordType === state.educationType) return;
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationType = recordType;
  state.educationEditorMode = "edit";
  state.selectedEducationId = statusEducationRecords()[0]?.id || null;
  closeCareerVaultCardDetail("education");
  renderEducation();
}

function beginNewEducation() {
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationVisibility = "active";
  state.educationEditorMode = "new";
  state.selectedEducationId = null;
  populateEducationForm(null);
  renderEducationList();
  openCareerVaultCardDetail("education");
  const focusTarget = state.educationType === "school"
    ? elements.educationInstitution
    : elements.certificationName;
  focusTarget.focus();
}

function selectEducationRecord(recordId) {
  if (recordId === state.selectedEducationId && state.educationEditorMode === "edit") {
    openCareerVaultCardDetail("education");
    return;
  }
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.selectedEducationId = recordId;
  state.educationEditorMode = "edit";
  populateEducationForm(selectedEducationRecord());
  renderEducationList();
  openCareerVaultCardDetail("education");
}

function cancelEducationEdit() {
  if (state.educationEditorMode === "new") {
    state.selectedEducationId = statusEducationRecords()[0]?.id || null;
    state.educationEditorMode = state.selectedEducationId ? "edit" : "new";
  }
  populateEducationForm(selectedEducationRecord());
  renderEducationList();
  elements.educationMessage.textContent = "Unsaved changes discarded.";
}

function setEducationVisibility(visibility) {
  if (visibility === state.educationVisibility) return;
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationVisibility = visibility;
  state.educationEditorMode = "edit";
  state.selectedEducationId = statusEducationRecords()[0]?.id || null;
  closeCareerVaultCardDetail("education");
  renderEducation();
}

function educationApiCollection() {
  return state.educationType === "school" ? "schools" : "certifications";
}

async function toggleEducationArchive() {
  const record = selectedEducationRecord();
  if (!record || state.educationEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(`Archive ${record.institution || record.name}? It will be excluded from new active workflows.`)) return;
  elements.archiveEducationButton.disabled = true;
  elements.saveEducationButton.disabled = true;
  elements.educationMessage.className = "form-message";
  elements.educationMessage.textContent = restoring ? "Restoring record..." : "Archiving record...";
  try {
    const result = await requestJson(
      `/api/vault/education/${educationApiCollection()}/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.educationRevision }),
      },
    );
    state.selectedEducationId = null;
    state.educationFormDirty = false;
    await loadVaultEducation({ keepMessage: true, refresh: true });
    elements.educationMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.educationMessage.className = "form-message error";
    elements.educationMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveEducationButton.disabled = false;
    elements.saveEducationButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultEducation(event) {
  event.preventDefault();
  const creating = state.educationEditorMode === "new";
  const recordId = state.selectedEducationId;
  const school = state.educationType === "school";
  const dates = educationDateRange.value();
  const graduation = graduationMonth.value();
  const completed = certificationCompletedMonth.value();
  const record = school
    ? {
      institution: elements.educationInstitution.value,
      degree: elements.educationDegree.value,
      major: elements.educationMajor.value,
      location: elements.educationLocation.value,
      ...dates,
      graduation_date: graduation.display,
      graduation_month: graduation.month,
      gpa: elements.educationGpa.value,
      status: elements.educationStatus.value,
      honors: vaultLines(elements.educationHonors.value),
    }
    : {
      name: elements.certificationName.value,
      credential: elements.certificationCredential.value,
      completed: completed.display,
      completed_month: completed.month,
      proof_points: vaultLines(elements.certificationProofPoints.value),
    };
  elements.saveEducationButton.disabled = true;
  elements.saveEducationButton.textContent = "Saving...";
  elements.educationMessage.className = "form-message";
  elements.educationMessage.textContent = "Validating canonical Career Vault...";
  try {
    const collection = educationApiCollection();
    const result = await requestJson(
      creating
        ? `/api/vault/education/${collection}`
        : `/api/vault/education/${collection}/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.educationRevision, record }),
      },
    );
    state.selectedEducationId = result.record.id;
    state.educationFormDirty = false;
    await loadVaultEducation({ keepMessage: true, refresh: true });
    elements.educationMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.educationMessage.className = "form-message error";
    elements.educationMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveEducationButton.disabled = false;
    elements.saveEducationButton.textContent = `Save ${educationTypeLabel()}`;
    await pollLogs();
  }
}
function renderSources() {
  const previousSource = elements.sourceSelect.value;
  elements.sourceSelect.innerHTML = state.sources.length
    ? state.sources.map((source) => (
      `<option value="${escapeHtml(source.name)}">${escapeHtml(source.name)} (${formatBytes(source.size_bytes)})</option>`
    )).join("")
    : '<option value="">No TXT or DOCX sources found</option>';
  if (state.sources.some((source) => source.name === previousSource)) {
    elements.sourceSelect.value = previousSource;
  }
  elements.runButton.disabled = state.sources.length === 0;
  elements.loadMoreSourcesButton.classList.toggle("hidden", !state.sourcesNextCursor);
  elements.loadMoreSourcesButton.textContent = `Load more sources (${state.sources.length}/${state.sourcesTotal})`;
  renderResumeImportsInProgress();
}

const MAX_RESUME_IMPORTS_IN_PROGRESS = 5;

function unfinishedResumeImports() {
  return state.extractions.filter((item) => (
    item.valid && item.workflow_status !== "saved_to_vault"
  ));
}

function renderResumeImportsInProgress() {
  const imports = unfinishedResumeImports().slice(0, MAX_RESUME_IMPORTS_IN_PROGRESS);
  elements.resumeImportsInProgress.hidden = imports.length === 0;
  if (!imports.length) return;
  const remaining = Math.max(0, unfinishedResumeImports().length - imports.length);
  elements.resumeImportsInProgressCount.textContent = remaining
    ? `${imports.length} shown / ${remaining} more`
    : `${imports.length} import${imports.length === 1 ? "" : "s"}`;
  elements.resumeImportsInProgressList.innerHTML = imports.map((item) => {
    const workflow = RESUME_WORKFLOW_STATUS[item.workflow_status]
      || RESUME_WORKFLOW_STATUS.extracted;
    const sourceName = item.source_name || item.id;
    return `<article class="resume-import-row">
      <div>
        <strong>${escapeHtml(sourceName)}</strong>
        <span>${escapeHtml(workflow.label)}: ${escapeHtml(resumeReviewSummary(item))}</span>
      </div>
      <button type="button" class="secondary-button compact-button" data-resume-review-id="${escapeHtml(item.id)}">Continue review</button>
    </article>`;
  }).join("");
}

function ensureExactReimportPanel() {
  let panel = document.querySelector("#exact-reimport-panel");
  if (panel) return panel;
  panel = document.createElement("section");
  panel.id = "exact-reimport-panel";
  panel.className = "completion-gate";
  panel.setAttribute("aria-live", "polite");
  panel.hidden = true;
  panel.innerHTML = `
    <div class="completion-heading">
      <div>
        <span class="completion-label">Existing import</span>
        <strong id="exact-reimport-title"></strong>
      </div>
      <div class="form-row">
        <button type="button" class="primary-button compact-button" id="open-previous-import-button">Open previous import</button>
        <button type="button" class="secondary-button compact-button" id="process-again-button">Process again</button>
      </div>
    </div>
    <p class="form-message" id="exact-reimport-message"></p>
    <ul class="plain-list" id="exact-reimport-list"></ul>
  `;
  elements.uploadMessage.insertAdjacentElement("afterend", panel);
  panel.querySelector("#open-previous-import-button").addEventListener(
    "click", openPreviousExactImport,
  );
  panel.querySelector("#process-again-button").addEventListener(
    "click", processExactReimport,
  );
  return panel;
}

function formatPriorImportTimestamp(value) {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.valueOf())
    ? "Earlier local import"
    : timestamp.toLocaleString();
}

function renderExactReimportPanel() {
  const panel = ensureExactReimportPanel();
  const check = state.sourceUploadCheck;
  const file = elements.sourceUpload.files[0];
  const currentCheck = check?.file === file ? check : null;
  const priorImports = currentCheck?.prior_imports || [];
  const sameNameConflict = currentCheck?.same_name_conflict === true;
  const title = panel.querySelector("#exact-reimport-title");
  const message = panel.querySelector("#exact-reimport-message");
  const list = panel.querySelector("#exact-reimport-list");
  const openButton = panel.querySelector("#open-previous-import-button");
  const processButton = panel.querySelector("#process-again-button");
  list.replaceChildren();
  openButton.hidden = true;
  processButton.hidden = true;
  if (!file || state.sourceUploadCheckLoading) {
    panel.hidden = true;
    elements.uploadButton.disabled = Boolean(file && state.sourceUploadCheckLoading);
    return;
  }
  if (currentCheck?.error) {
    panel.hidden = false;
    title.textContent = "Could not check this file";
    message.textContent = "Choose the file again before adding it; no local file was created.";
    elements.uploadButton.disabled = true;
    return;
  }
  if (priorImports.length) {
    panel.hidden = false;
    title.textContent = "This exact file was processed before";
    message.textContent = "Open the existing review, or explicitly create a separate new review.";
    priorImports.forEach((item, index) => {
      const status = RESUME_WORKFLOW_STATUS[item.workflow_status]
        || RESUME_WORKFLOW_STATUS.extracted;
      const row = document.createElement("li");
      row.textContent = `${index === 0 ? "Most recent" : "Earlier"} import: ${status.label} — ${formatPriorImportTimestamp(item.processed_at)}`;
      list.append(row);
    });
    openButton.hidden = false;
    processButton.hidden = false;
    elements.uploadButton.disabled = true;
    return;
  }
  if (sameNameConflict) {
    panel.hidden = false;
    title.textContent = "A different local file already uses this name";
    message.textContent = "It will not be replaced. Rename this file, choose it again, then add it as a new import.";
    elements.uploadButton.disabled = true;
    return;
  }
  panel.hidden = true;
  elements.uploadButton.disabled = false;
}

function clearSourceUploadCheck() {
  state.sourceUploadCheck = null;
  state.sourceUploadCheckLoading = false;
  renderExactReimportPanel();
}

async function inspectUploadedDocument() {
  const file = elements.sourceUpload.files[0];
  state.sourceUploadCheck = null;
  if (!file) {
    renderExactReimportPanel();
    return;
  }
  state.sourceUploadCheckLoading = true;
  renderExactReimportPanel();
  elements.uploadMessage.className = "form-message upload-message";
  elements.uploadMessage.textContent = `Checking ${file.name} against prior imports...`;
  try {
    const result = await requestJson("/api/upload/check", {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Source-Name": encodeURIComponent(file.name),
      },
      body: file,
    });
    if (elements.sourceUpload.files[0] !== file) return;
    state.sourceUploadCheck = { file, ...result };
    elements.uploadMessage.textContent = result.prior_imports?.length
      ? "An exact prior import was found. Choose how to continue."
      : "Ready to add locally.";
  } catch (error) {
    if (elements.sourceUpload.files[0] !== file) return;
    state.sourceUploadCheck = { file, error: formatRequestError(error) };
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = formatRequestError(error);
  } finally {
    if (elements.sourceUpload.files[0] === file) {
      state.sourceUploadCheckLoading = false;
      renderExactReimportPanel();
    }
  }
}

async function openPreviousExactImport() {
  const file = elements.sourceUpload.files[0];
  const priorImport = state.sourceUploadCheck?.file === file
    ? state.sourceUploadCheck.prior_imports?.[0]
    : null;
  if (!priorImport?.source_id) return;
  elements.sourceUpload.value = "";
  clearSourceUploadCheck();
  await openResumeReview(priorImport.source_id);
}

async function processExactReimport() {
  const file = elements.sourceUpload.files[0];
  const priorImports = state.sourceUploadCheck?.file === file
    ? state.sourceUploadCheck.prior_imports
    : [];
  if (!priorImports?.length) return;
  await uploadDocument({ processAgain: true });
}
const RESUME_WORKFLOW_STATUS = {
  extracted: { label: "Needs review", className: "status-warning" },
  in_review: { label: "In review", className: "status-warning" },
  vault_decision: { label: "Vault decision", className: "status-warning" },
  ready_to_save: { label: "Ready to save", className: "status-success" },
  saved_to_vault: { label: "Saved to Vault", className: "status-success" },
  review_error: { label: "Review error", className: "status-error" },
};

function resumeReviewSummary(item) {
  if (item.workflow_status === "saved_to_vault") {
    return `${item.review_entity_count || 0} saved decisions`;
  }
  if (item.workflow_status === "extracted") return "Review not started";
  if (item.workflow_status === "review_error") return "Needs inspection";
  const cards = `${item.review_entity_count || 0} card${item.review_entity_count === 1 ? "" : "s"}`;
  const unresolved = Number(item.unresolved_block_count || 0);
  if (unresolved) return `${cards} / ${unresolved} unresolved`;
  const decisions = Number(item.approval_issue_count || 0);
  return decisions ? `${cards} / ${decisions} decisions` : cards;
}

function renderExtractions() {
  renderResumeImportsInProgress();
}

function resumeLibraryRecordsForVisibility() {
  const archived = state.resumeLibraryVisibility === "archived";
  return state.resumeLibraryManagement.filter((resume) => (resume.archived === true) === archived);
}

function resumeUsageLabel(usage = {}) {
  const references = [
    Number(usage.current_application_selections || 0),
    Number(usage.applied_resume_snapshots || 0),
    Number(usage.submitted_resume_events || 0),
  ].reduce((total, value) => total + value, 0);
  if (!references) return "Not used in an application yet";
  return `${references} application reference${references === 1 ? "" : "s"}`;
}

function filteredResumeLibraryRecords() {
  const query = elements.resumeLibrarySearch.value.trim().toLocaleLowerCase();
  const digestFilter = state.resumeLibraryDigestFilter;
  const records = resumeLibraryRecordsForVisibility().filter((resume) => (
    (!query || String(resume.display_name || "").toLocaleLowerCase().includes(query))
    && (digestFilter === "all" || resume.digest_status === digestFilter)
  ));
  return records.sort((left, right) => {
    if (state.resumeLibrarySort === "oldest") return String(left.added_at).localeCompare(String(right.added_at));
    if (state.resumeLibrarySort === "name_asc") return left.display_name.localeCompare(right.display_name);
    if (state.resumeLibrarySort === "name_desc") return right.display_name.localeCompare(left.display_name);
    if (state.resumeLibrarySort === "digest") return String(left.digest_status).localeCompare(String(right.digest_status)) || left.display_name.localeCompare(right.display_name);
    return String(right.added_at).localeCompare(String(left.added_at));
  });
}

const RESUME_DIGEST_STATES = {
  not_digested: { label: "Not reviewed", className: "is-neutral" },
  in_progress: { label: "Review in progress", className: "is-progress" },
  digested: { label: "Reviewed", className: "is-complete" },
  outdated: { label: "Update available", className: "is-outdated" },
  error: { label: "Needs attention", className: "is-error" },
};

function resumeLibraryEmptyState(recordsInVisibility) {
  const query = elements.resumeLibrarySearch.value.trim();
  if (query) {
    return `<div class="empty-state">No ${state.resumeLibraryVisibility} Resume Versions match "${escapeHtml(query)}".</div>`;
  }
  if (state.resumeLibraryVisibility === "archived") {
    return '<div class="empty-state">No archived Resume Versions. Archived materials remain available in application history.</div>';
  }
  if (recordsInVisibility) {
    return '<div class="empty-state">No active Resume Versions match this view.</div>';
  }
  return `
    <div class="empty-state resume-library-empty-state">
      <strong>No active Resume Versions yet.</strong>
      <p>Add a resume, then choose <strong>Save for applications</strong> to keep it ready for your next application.</p>
      <button type="button" class="primary-button" data-add-application-resume>Add resume</button>
    </div>`;
}

function renderResumeLibrary() {
  const unavailable = state.resumeLibraryManagementError;
  const recordsInVisibility = resumeLibraryRecordsForVisibility();
  const records = filteredResumeLibraryRecords();
  const activeCount = state.resumeLibraryManagement.filter((resume) => resume.archived !== true).length;
  const archivedCount = state.resumeLibraryManagement.length - activeCount;

  elements.resumeLibraryPanel.classList.toggle(
    "is-empty",
    !unavailable && state.resumeLibraryManagement.length === 0,
  );

  elements.resumeLibraryCount.textContent = unavailable
    ? "Resume list unavailable"
    : `${activeCount} active version${activeCount === 1 ? "" : "s"}`;
  elements.resumeLibraryArchivedCount.textContent = unavailable ? "–" : archivedCount;
  elements.resumeLibraryArchivedFilter.disabled = unavailable;
  updateArchiveScopeToggle(
    elements.resumeLibraryArchivedFilter,
    state.resumeLibraryVisibility === "archived",
    "Resume Versions",
  );

  if (unavailable) {
    elements.resumeLibraryRecordList.innerHTML = "";
    return;
  }

  elements.resumeLibraryRecordList.innerHTML = records.length
    ? records.map((resume) => {
      const archived = resume.archived === true;
      const fileType = String(resume.file_type || "File").toUpperCase();
      const availability = archived
        ? "Archived - retained for existing history"
        : "Available for new applications";
      const mutating = state.resumeLibraryMutationId === resume.id;
      const mutationAction = mutating ? state.resumeLibraryMutationAction : null;
      const action = archived ? "restore" : "archive";
      const actionLabel = mutating && mutationAction === action
        ? (archived ? "Restoring..." : "Archiving...")
        : (archived ? "Restore" : "Archive");
      const actionTooltip = archived
        ? "Restore this Resume Version so it appears in new application pickers again."
        : "Hide this Resume Version from new application pickers. Existing application history remains intact.";
      const digestState = RESUME_DIGEST_STATES[resume.digest_status] || RESUME_DIGEST_STATES.not_digested;
      const digestRouteAvailable = api.capabilities.resume_digest !== false;
      const canDigest = digestRouteAvailable && !archived && resume.digest_supported === true;
      const continueReview = canDigest && resume.digest_status === "in_progress" && resume.digest_source_id;
      const digestAction = continueReview ? "continue-digest" : "digest";
      const digestLabel = !digestRouteAvailable
        ? "Review unavailable"
        : continueReview
        ? "Continue review"
        : resume.digest_status === "not_digested" ? "Review" : "Review again";
      const digestTooltip = !digestRouteAvailable
        ? "Resume review is not yet connected in this preview. The saved version remains available for applications."
        : !resume.digest_supported
        ? "Local review currently supports TXT and DOCX resume files."
        : archived
          ? "Restore this resume before reviewing it."
          : continueReview
            ? "Continue the existing local review without analyzing the file again."
            : "Create a new local deterministic review without changing prior reviews or Career Vault.";
      const deleteButton = resume.deletable === true
        ? `<button type="button" class="secondary-button compact-button"
            data-resume-library-action="delete"
            data-resume-library-id="${escapeHtml(resume.id)}"
            data-tooltip="Permanently delete this unused Resume Version and its stored file. This cannot be undone."
            ${mutating ? "disabled" : ""}>${mutationAction === "delete" ? "Deleting..." : "Delete"}</button>`
        : "";
      const downloadLabel = mutating && mutationAction === "download"
        ? "Downloading..."
        : "Download";
      return `
        <article class="resume-library-record${state.resumeLibraryFocusId === resume.id ? " is-route-target" : ""}"
          data-resume-library-record-id="${escapeHtml(resume.id)}" tabindex="-1">
          <div class="resume-library-record-heading">
            <strong translate="no">${escapeHtml(resume.display_name)}</strong>
            <span class="resume-library-state ${archived ? "is-archived" : "is-active"}">${archived ? "Archived" : "Active"}</span>
          </div>
          <div class="resume-library-record-meta">
            <span>Added ${escapeHtml(formatTime(resume.added_at))}</span>
            <span>${escapeHtml(fileType)} / ${escapeHtml(formatBytes(Number(resume.size_bytes)))}</span>
            <span class="resume-digest-state ${digestState.className}">${escapeHtml(digestState.label)}</span>
          </div>
          <div class="resume-library-record-footer">
            <div class="resume-library-record-summary">
              <span>${escapeHtml(resumeUsageLabel(resume.usage))}</span>
              <span>${availability}</span>
            </div>
            <div class="resume-library-record-actions">
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="download"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-tooltip="Download this exact immutable Resume Version for the current application."
                ${mutating ? "disabled" : ""}>${downloadLabel}</button>
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="${digestAction}"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-resume-digest-source-id="${escapeHtml(resume.digest_source_id || "")}"
                data-tooltip="${digestTooltip}"
                ${!canDigest || mutating ? "disabled" : ""}>${mutationAction === "digest" ? "Reviewing..." : digestLabel}</button>
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="${action}"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-tooltip="${actionTooltip}"
                ${mutating ? "disabled" : ""}>${actionLabel}</button>
              ${deleteButton}
            </div>
          </div>
        </article>`;
    }).join("")
    : resumeLibraryEmptyState(recordsInVisibility.length);
  focusResumeLibraryRouteTarget();
}

function focusResumeLibraryRouteTarget() {
  const resumeId = state.resumeLibraryFocusId;
  if (!resumeId || state.activeView !== "resume-library") return;
  const target = Array.from(
    elements.resumeLibraryRecordList.querySelectorAll("[data-resume-library-record-id]")
  ).find((record) => record.dataset.resumeLibraryRecordId === resumeId);
  if (!target) {
    if (resumeLibraryManagementLoaded) {
      state.resumeLibraryFocusId = null;
      elements.resumeLibraryMessage.className = "form-message error";
      elements.resumeLibraryMessage.textContent = "That Resume Version is no longer available in the library.";
    }
    return;
  }
  state.resumeLibraryFocusId = null;
  window.requestAnimationFrame(() => {
    target.scrollIntoView({ behavior: "smooth", block: "center" });
    target.focus({ preventScroll: true });
  });
}

function setResumeLibraryVisibility(visibility) {
  state.resumeLibraryVisibility = visibility;
  renderResumeLibrary();
}

async function digestResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || resume.archived || !resume.digest_supported
    || api.capabilities.resume_digest === false || state.resumeLibraryMutationId) return;
  if (resume.digest_status !== "not_digested" && !window.confirm(
    `Digest "${resume.display_name}" again? A new review will be created. Existing reviews and Career Vault records will not be overwritten.`
  )) return;
  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "digest";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Digesting ${resume.display_name} locally...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(`/api/resumes/${encodeURIComponent(resumeId)}/digest`, {
      method: "POST",
    });
    await prepareReviewForSource(result.source_id);
    resumeLibraryManagementLoaded = false;
    await loadResumeCollections({ force: true });
    await openResumeReview(result.source_id);
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

async function downloadResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || state.resumeLibraryMutationId) return;
  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "download";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Preparing ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const { blob, filename } = await api.downloadResumeVersion(resumeId);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.resumeLibraryMessage.textContent = `${filename} downloaded.`;
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}
function todayInputValue() {
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 10);
}

function filteredApplicationJobs() {
  return filterApplicationJobs(state.jobs, {
    query: elements.applicationLedgerSearch.value,
    status: elements.applicationLedgerStatusFilter.value,
    scope: state.applicationLedgerScope,
  });
}

function renderApplicationLedger() {
  const jobs = filteredApplicationJobs();
  const applicationCount = state.jobs.filter((job) => (
    job.valid && hasApplicationRecord(job.application)
  )).length;
  elements.applicationLedgerCount.textContent = jobs.length.toLocaleString()
    + " shown / " + applicationCount.toLocaleString() + " saved jobs";
  document.querySelectorAll("[data-application-scope]").forEach((button) => {
    button.setAttribute("aria-pressed", String(button.dataset.applicationScope === state.applicationLedgerScope));
  });
  elements.applicationLedgerList.innerHTML = renderApplicationLedgerRows(jobs, {
    selectedJobId: state.selectedJobId,
    drawerOpen: state.applicationDrawerOpen,
  });
}

function renderApplicationEmailUpdates() {
  const snapshot = state.applicationEmailUpdates || {};
  const items = Array.isArray(snapshot.items) ? snapshot.items : [];
  elements.applicationEmailUpdates.classList.toggle("hidden", !items.length);
  const needsReview = Number(snapshot.needs_review || 0);
  elements.applicationEmailUpdatesCount.textContent = items.length
    ? `${Number(snapshot.total || items.length).toLocaleString()} total${needsReview ? ` · ${needsReview.toLocaleString()} need review` : ""}`
    : "";
  elements.applicationEmailUpdatesList.innerHTML = renderApplicationEmailUpdateRows(
    items,
    state.jobs,
  );
  elements.applicationEmailUpdatesMore.classList.toggle(
    "hidden",
    !state.applicationEmailUpdatesNextCursor,
  );
  elements.applicationEmailUpdatesMore.disabled = state.applicationEmailUpdatesLoading;
  elements.applicationEmailUpdatesMore.textContent = state.applicationEmailUpdatesLoading
    ? "Loading…"
    : "Load more email updates";
  if (state.applicationEmailUpdateMutationId) {
    elements.applicationEmailUpdatesList
      .querySelectorAll(`[data-outcome-id="${CSS.escape(state.applicationEmailUpdateMutationId)}"]`)
      .forEach((button) => {
        button.disabled = true;
        const select = button.closest(".application-email-update-linker")?.querySelector("select");
        if (select) select.disabled = true;
      });
  }
}

function renderApplicationSessionReviews() {
  const snapshot = state.applicationSessionReviews || {};
  const items = Array.isArray(snapshot.items) ? snapshot.items : [];
  elements.applicationSessionReviews.classList.toggle("hidden", !items.length);
  elements.applicationSessionReviewsCount.textContent = items.length
    ? `${items.length.toLocaleString()} awaiting confirmation`
    : "";
  elements.applicationSessionReviewList.innerHTML = renderApplicationSessionReviewRows(items);
  if (state.applicationSessionMutationId) {
    elements.applicationSessionReviewList
      .querySelectorAll(`[data-application-session-id="${CSS.escape(state.applicationSessionMutationId)}"] button`)
      .forEach((button) => { button.disabled = true; });
  }
}

async function loadApplicationSessionReviews() {
  if (state.applicationSessionReviewsLoading) return;
  state.applicationSessionReviewsLoading = true;
  try {
    state.applicationSessionReviews = await api.getApplicationSessionReviews();
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionReviewsLoading = false;
    renderApplicationSessionReviews();
  }
}

async function resolveApplicationSessionReview(sessionId, action, expectedRevision) {
  if (state.applicationSessionMutationId) return;
  state.applicationSessionMutationId = sessionId;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = action === "confirm"
    ? "Recording the confirmed submission..."
    : "Leaving the application stage unchanged...";
  renderApplicationSessionReviews();
  renderJobApplication();
  try {
    const result = await api.resolveApplicationSession(
      sessionId,
      action,
      Number(expectedRevision),
    );
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationLedgerMessage.className = "form-message application-ledger-message success";
    elements.applicationLedgerMessage.textContent = action === "confirm"
      ? `Recorded ${result.session.company || result.session.title} as Applied.`
      : "Submission signal dismissed. The application stage was not changed.";
    toast.show({
      message: action === "confirm"
        ? "Application marked Applied after confirmation."
        : "Submission signal dismissed.",
    });
    state.applicationSessionReviews = null;
    await loadApplicationSessionReviews();
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
    renderApplicationSessionReviews();
  }
}

function clearApplicationEmailUpdates() {
  state.applicationEmailUpdates = null;
  state.applicationEmailUpdatesNextCursor = null;
}

async function loadApplicationEmailUpdates({ append = false } = {}) {
  if (state.applicationEmailUpdatesLoading) return;
  const cursor = append ? state.applicationEmailUpdatesNextCursor : null;
  if (append && !cursor) return;
  state.applicationEmailUpdatesLoading = true;
  renderApplicationEmailUpdates();
  try {
    const snapshot = await api.getApplicationEmailUpdatesPage({ cursor });
    const priorItems = append && Array.isArray(state.applicationEmailUpdates?.items)
      ? state.applicationEmailUpdates.items
      : [];
    state.applicationEmailUpdates = {
      ...snapshot,
      items: [...priorItems, ...(snapshot.items || [])],
    };
    state.applicationEmailUpdatesNextCursor = snapshot.next_cursor || null;
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationEmailUpdatesLoading = false;
    renderApplicationEmailUpdates();
  }
}

async function linkApplicationEmailUpdate(button) {
  if (state.applicationEmailUpdateMutationId) return;
  const linker = button.closest(".application-email-update-linker");
  const select = linker?.querySelector("[data-email-update-job]");
  const jobId = select?.value || "";
  if (!jobId) return;
  const outcomeId = button.dataset.outcomeId;
  state.applicationEmailUpdateMutationId = outcomeId;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = "Linking the email update to the selected application…";
  renderApplicationEmailUpdates();
  try {
    const result = await api.linkApplicationEmailUpdate(
      outcomeId,
      jobId,
      Number(button.dataset.outcomeRevision),
    );
    storeJobApplication(result.application);
    clearApplicationEmailUpdates();
    await loadApplicationEmailUpdates();
    elements.applicationLedgerMessage.className = "form-message application-ledger-message success";
    elements.applicationLedgerMessage.textContent = result.update.application_changed
      ? `Email confirmation linked and application moved to ${applicationStatusLabel(result.application.current_status)}.`
      : "Email confirmation linked. The application already had this or a later stage.";
    toast.show({ message: "Email update linked to the application." });
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT") {
      clearApplicationEmailUpdates();
      await loadApplicationEmailUpdates();
    }
  } finally {
    state.applicationEmailUpdateMutationId = null;
    renderJobs();
    renderApplicationLedger();
    renderApplicationEmailUpdates();
  }
}

function renderJobLibraryApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = job?.application;
  const status = hasApplicationRecord(application)
    ? applicationStatusLabel(application.current_status)
    : "Not tracked";
  elements.jobLibraryApplicationBar.innerHTML = renderActionBar({
    label: "Application status",
    className: "job-library-action-bar",
    summary: `<div class="job-library-application-summary"><span>Application status</span>${renderStatusBadge({ label: status })}</div>`,
    actions: [
      {
        label: "Open in Applications",
        variant: "primary",
        attributes: {
          "data-open-selected-application": "true",
          "data-tooltip": "Open this saved job in the application ledger and update its status when needed.",
        },
        disabled: !job?.valid || !hasApplicationRecord(application) || state.jobApplicationLoading,
      },
    ],
  });
}

function storeJobApplication(application) {
  const job = state.jobs.find((item) => item.id === application?.job_id);
  if (job) job.application = application;
  if (application?.assessment && application.job_id === state.selectedJobId) {
    state.jobAssessment = application.assessment;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
  }
  if (application?.job_id === state.selectedJobId) state.jobApplication = application;
}

async function loadResumeLibrary() {
  let loaded = false;
  try {
    const result = await api.listResumeVersions();
    state.resumeLibrary = Array.isArray(result.resumes) ? result.resumes : [];
    loaded = true;
  } catch (error) {
    state.resumeLibrary = [];
    state.applicationResumeMessageJobId = state.selectedJobId;
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    renderJobWorkspaceMaterials();
    renderJobApplication();
  }
  return loaded;
}

function invalidateResumeLibraryManagement() {
  resumeLibraryManagementLoaded = false;
}

async function loadResumeLibraryManagement({ force = false } = {}) {
  let loaded = true;
  if (!resumeLibraryManagementLoaded || force) {
    state.resumeLibraryManagementError = false;
    elements.resumeLibraryMessage.className = "form-message";
    elements.resumeLibraryMessage.textContent = "";
    try {
      const result = await api.listResumeVersions({ includeArchived: true });
      state.resumeLibraryManagement = Array.isArray(result.resumes) ? result.resumes : [];
      resumeLibraryManagementLoaded = true;
    } catch (error) {
      loaded = false;
      resumeLibraryManagementLoaded = false;
      state.resumeLibraryManagementError = true;
      state.resumeLibraryManagement = [];
      elements.resumeLibraryMessage.className = "form-message error";
      elements.resumeLibraryMessage.textContent = formatRequestError(error);
    }
  }
  renderResumeLibrary();
  return loaded;
}

async function setResumeLibraryArchiveState(resumeId, archived) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || state.resumeLibraryMutationId === resumeId) return;
  const action = archived ? "archive" : "restore";
  if (archived) {
    const confirmed = window.confirm(
      `Archive ${resume.display_name}? It will be hidden from new application pickers. Existing application history will remain available.`
    );
    if (!confirmed) return;
  }

  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = action;
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = archived
    ? `Archiving ${resume.display_name}...`
    : `Restoring ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(
      `/api/resumes/${encodeURIComponent(resumeId)}/${action}`,
      { method: "POST" }
    );
    if (result.archived !== archived) {
      throw new Error(`Resume archive state did not update to ${archived}.`);
    }
    const [pickerLoaded, managementLoaded] = await Promise.all([
      loadResumeLibrary(),
      loadResumeLibraryManagement({ force: true }),
    ]);
    if (!pickerLoaded || !managementLoaded) {
      throw new Error(
        "Archive state changed, but the Resume Library could not be refreshed. Reload before continuing."
      );
    }
    elements.resumeLibraryMessage.className = "form-message success";
    elements.resumeLibraryMessage.textContent = archived
      ? `${resume.display_name} archived. It is hidden from new application pickers and remains in existing application history.`
      : `${resume.display_name} restored. It is available in the active library and new application pickers.`;
    toast.show({
      message: archived
        ? `Resume archived: ${resume.display_name}.`
        : `Resume restored: ${resume.display_name}.`,
    });
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

async function deleteResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (
    !resume
    || resume.deletable !== true
    || state.resumeLibraryMutationId === resumeId
  ) return;
  const confirmed = window.confirm(
    `Permanently delete "${resume.display_name}"? This permanently removes the stored Resume Version file. This action cannot be undone. Career Vault data is not affected.`
  );
  if (!confirmed) return;

  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "delete";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Deleting ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(
      `/api/resumes/${encodeURIComponent(resumeId)}`,
      { method: "DELETE" }
    );
    if (result.deleted !== true || result.id !== resumeId) {
      throw new Error("Resume Version deletion was not confirmed by the server.");
    }
    const [pickerLoaded, managementLoaded] = await Promise.all([
      loadResumeLibrary(),
      loadResumeLibraryManagement({ force: true }),
    ]);
    if (!pickerLoaded || !managementLoaded) {
      throw new Error(
        "Resume Version was deleted, but the Resume Library could not be refreshed. Reload before continuing."
      );
    }
    elements.resumeLibraryMessage.className = "form-message success";
    elements.resumeLibraryMessage.textContent = `${resume.display_name} was permanently deleted. Career Vault data was not changed.`;
    toast.show({ message: `Resume deleted: ${resume.display_name}.` });
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

async function selectApplicationResume(resumeId) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.applicationResumeLoading) return;
  state.applicationResumeLoading = true;
  state.applicationResumeMessageJobId = job.id;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = resumeId
    ? "Selecting resume..."
    : "Recording no resume...";
  renderJobWorkspaceMaterials();
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/resume-selection`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ resume_id: resumeId || null }),
      }
    );
    storeJobApplication(application);
    const selected = application.resume_selection?.resume;
    elements.applicationResumeMessage.textContent = selected
      ? `Selected ${selected.display_name}.`
      : "Resume marked as not recorded.";
    toast.show({
      message: selected
        ? `Resume selected: ${selected.display_name}.`
        : "Resume marked as not recorded.",
    });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function openSelectedApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !hasApplicationRecord(job.application) || state.jobApplicationLoading) return;
  elements.jobLibraryMessage.className = "form-message job-library-application-message";
  elements.jobLibraryMessage.textContent = "";
  switchView("applications");
  await selectApplicationJob(job.id);
}

async function selectApplicationJob(jobId) {
  if (!state.jobs.some((job) => (
    job.valid && job.id === jobId && hasApplicationRecord(job.application)
  ))) return;
  state.selectedJobId = jobId;
  state.applicationDrawerOpen = true;
  state.jobApplication = null;
  state.jobApplicationError = "";
  state.applicationResumeLoading = false;
  elements.applicationResumeFile.value = "";
  elements.applicationResumeDisplayName.value = "";
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = "";
  renderJobs();
  await Promise.all([loadResumeLibrary(), loadSelectedJobApplication()]);
}
function closeApplicationDrawer() {
  state.applicationDrawerOpen = false;
  document.body.classList.remove("application-drawer-open");
  renderApplicationLedger();
  renderJobApplication();
}

async function openSelectedJobInLibrary() {
  const jobId = state.selectedJobId;
  closeApplicationDrawer();
  switchView("job-library");
  if (jobId) await selectJob(jobId);
}

function coverLetterSummary(editor) {
  return {
    persisted: Boolean(editor?.persisted),
    revision: Number(editor?.revision || 0),
    status: editor?.draft?.status || "draft",
    ready: Boolean(editor?.validation?.ready_to_export),
  };
}

async function ensureCoverLetterSummary(jobId) {
  if (!jobId || state.coverLetterSummaries[jobId]) return;
  state.coverLetterSummaries[jobId] = { loading: true };
  renderJobWorkspaceMaterials();
  try {
    const editor = await api.getCoverLetter(jobId);
    state.coverLetterSummaries[jobId] = coverLetterSummary(editor);
  } catch (error) {
    state.coverLetterSummaries[jobId] = {
      error: formatRequestError(error),
    };
  }
  if (state.selectedJobId === jobId) renderJobWorkspaceMaterials();
}

function renderJobWorkspaceMaterials() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication?.job_id === job?.id
    ? state.jobApplication
    : job?.application;
  const available = Boolean(job?.valid && hasApplicationRecord(application));
  elements.jobWorkspaceMaterials.classList.toggle("hidden", !available);
  if (!available) return;
  if (state.applicationResumeMessageJobId !== job.id) {
    state.applicationResumeMessageJobId = job.id;
    elements.applicationResumeMessage.className = "form-message application-resume-message";
    elements.applicationResumeMessage.textContent = "";
  }

  const resumePlan = application.resume_plan;
  const canManageMaterials = canManageApplicationMaterials(application);
  elements.jobWorkspaceMaterialsStatus.textContent = "Ready";

  const resumePlanApproved = resumePlan?.status === "approved";
  elements.applicationResumeMaterialState.textContent = resumePlanApproved
    ? `Content selected / revision ${resumePlan.revision}`
    : resumePlan
      ? "Tailoring in progress"
      : "Not tailored";
  elements.applicationResumeMaterialGuidance.textContent = resumePlanApproved
    ? "Review application-only wording and export DOCX, PDF, or TXT from the same workspace."
    : "Select truthful Career Vault evidence and edit only where the job benefits.";
  elements.openResumeMaterialButton.textContent = resumePlanApproved
    ? "Continue tailoring"
    : "Tailor resume";
  elements.openResumeMaterialButton.disabled = state.jobApplicationLoading || !canManageMaterials;

  const coverLetter = state.coverLetterSummaries[job.id];
  if (!coverLetter) void ensureCoverLetterSummary(job.id);
  elements.applicationCoverLetterState.textContent = coverLetter?.loading
    ? "Loading"
    : coverLetter?.error
      ? "Unavailable"
      : coverLetter?.persisted
        ? `${coverLetter.status} / revision ${coverLetter.revision}`
        : "Not started";
  elements.applicationCoverLetterGuidance.textContent = coverLetter?.ready
    ? "Saved and validated. Open it to preview or export DOCX, PDF, or TXT."
    : coverLetter?.persisted
      ? "A draft exists. Resolve readiness checks before export."
      : "Start from the standard structure, then tailor only evidence and job-specific prose.";
  elements.openCoverLetterButton.textContent = coverLetter?.persisted
    ? "Edit cover letter"
    : "Create cover letter";
  elements.openCoverLetterButton.disabled = (
    state.jobApplicationLoading || !canManageMaterials || coverLetter?.loading
  );

  const selectedResume = application.resume_selection?.resume;
  applicationResumeCombobox.setOptions(state.resumeLibrary.map((resume) => ({
    value: resume.id,
    label: resume.display_name,
  })));
  applicationResumeCombobox.setValue(selectedResume?.id || "");
  const resumeControlsDisabled = state.applicationResumeLoading || state.jobApplicationLoading;
  applicationResumeCombobox.setDisabled(resumeControlsDisabled);
  elements.clearApplicationResumeSelectionButton.disabled = resumeControlsDisabled;
  elements.clearApplicationResumeSelectionButton.classList.toggle("hidden", !selectedResume);
  const appliedResume = application.applied_resume;
  const appliedResumeHistory = appliedResume?.history || [];
  const appliedResumeCorrected = appliedResumeHistory.some((event) => event.action === "corrected");
  const appliedResumeVersion = appliedResume?.recorded ? appliedResume.resume : null;
  const latestSubmittedEvent = appliedResumeHistory.at(-1);
  const canOpenSubmittedFile = Boolean(
    appliedResumeVersion?.id && latestSubmittedEvent?.resume?.id === appliedResumeVersion.id
  );
  const selectedMatchesSubmitted = Boolean(selectedResume?.id && selectedResume.id === appliedResumeVersion?.id);
  elements.applicationResumeState.textContent = selectedResume ? "Selected for tailoring" : "";
  elements.applicationResumeState.classList.toggle("hidden", !selectedResume);
  elements.applicationResumeLabel.textContent = appliedResumeVersion?.display_name || "Not recorded";
  elements.applicationResumeMeta.textContent = appliedResumeVersion
    ? (appliedResumeCorrected
      ? "Recorded for this application. Earlier corrections remain in local history."
      : "Recorded as the resume used for this application.")
    : appliedResume?.recorded
      ? "No resume was recorded when this application was marked Applied."
      : "Record the version you actually sent after the application reaches Applied.";
  elements.openApplicationResume.classList.toggle("hidden", !canOpenSubmittedFile);
  const canRecordSubmittedResume = Boolean(appliedResume?.recorded && selectedResume && !selectedMatchesSubmitted);
  elements.recordSubmittedResumeButton.classList.toggle("hidden", !canRecordSubmittedResume);
  elements.recordSubmittedResumeButton.disabled = resumeControlsDisabled || !selectedResume;
  elements.recordSubmittedResumeButton.textContent = appliedResumeVersion
    ? "Use selected as submitted"
    : "Record selected as submitted";
  elements.applicationResumeFile.disabled = resumeControlsDisabled;
  elements.applicationResumeDisplayName.disabled = resumeControlsDisabled;
  elements.attachApplicationResumeButton.disabled = resumeControlsDisabled;
  elements.attachApplicationResumeButton.textContent = state.applicationResumeLoading
    ? "Uploading..."
    : "Upload and select";
}

function renderApplicationSessionPanel(job, application) {
  const session = application?.application_session || null;
  const stateLabel = {
    pending_claim: "Waiting for posting",
    armed: "Tracking tab",
    submission_detected: "Confirmation required",
    confirmed_applied: "Applied confirmed",
    dismissed: "Signal dismissed",
    interrupted: "Tracking interrupted",
    expired: "Expired",
    cancelled: "Cancelled",
  }[session?.state] || "Not started";
  elements.applicationSessionState.textContent = stateLabel;
  elements.applicationSessionState.dataset.state = session?.state || "idle";

  const reasonCopy = {
    origin_changed: "The tab left the site used when it was armed.",
    tab_closed: "The attended tab was closed before a confirmed submission.",
    tab_reused: "That browser tab was armed for another application.",
    instrumentation_lost: "The extension could no longer observe the attended tab.",
    timeout: "The four-hour session expired without a confirmed submission.",
    replaced: "A newer session replaced this one.",
    manual_stop: "You ended this session.",
  };
  if (session?.state === "pending_claim") {
    elements.applicationSessionGuidance.textContent = "Open the exact posting, then choose Track submission in the Career Hub extension. The tracking link expires after four hours.";
  } else if (session?.state === "armed") {
    elements.applicationSessionGuidance.textContent = "Apply normally in the armed tab. Only a native submit followed by a recognized success state can create a confirmation request; no form values are recorded.";
  } else if (session?.state === "submission_detected") {
    elements.applicationSessionGuidance.textContent = "A likely successful submission is waiting at the top of Applications. The stage is unchanged until you confirm it.";
  } else if (["interrupted", "expired", "cancelled"].includes(session?.state)) {
    elements.applicationSessionGuidance.textContent = reasonCopy[session.interruption_reason]
      || "This session ended without changing the application stage. Start again if you are still applying.";
  } else if (session?.state === "confirmed_applied") {
    elements.applicationSessionGuidance.textContent = "You confirmed the detected submission. The application history keeps the exact Applied event.";
  } else if (session?.state === "dismissed") {
    elements.applicationSessionGuidance.textContent = "You dismissed the detected signal and the application stage was left unchanged.";
  } else {
    elements.applicationSessionGuidance.textContent = "From the posting, choose Track submission in the extension. It starts only when one saved job matches the exact URL; no form values are recorded. Create a tracking link here only as a fallback.";
  }

  const canStart = Boolean(
    job?.source_url
    && ["captured", "reviewed", "preparing"].includes(application?.current_status)
    && session?.state !== "submission_detected"
    && !state.applicationSessionMutationId
  );
  const reviewMode = session?.state === "submission_detected";
  elements.startApplicationSessionButton.dataset.mode = reviewMode ? "review" : "start";
  elements.startApplicationSessionButton.textContent = reviewMode
    ? "Review confirmation"
    : session
      ? "Create fresh link"
      : "Create tracking link";
  elements.startApplicationSessionButton.disabled = reviewMode
    ? false
    : !canStart;

  const canOpen = Boolean(job?.source_url && ["pending_claim", "armed"].includes(session?.state));
  elements.openApplicationSessionPosting.href = canOpen ? job.source_url : "#";
  elements.openApplicationSessionPosting.classList.toggle("hidden", !canOpen);
  const canCancel = ["pending_claim", "armed", "interrupted"].includes(session?.state);
  elements.cancelApplicationSessionButton.classList.toggle("hidden", !canCancel);
  elements.cancelApplicationSessionButton.disabled = Boolean(state.applicationSessionMutationId);
}

function renderJobApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const visible = state.activeView === "applications"
    && state.applicationDrawerOpen
    && job?.valid
    && hasApplicationRecord(job.application);
  elements.jobApplicationSection.classList.toggle("hidden", !visible);
  elements.applicationDrawerBackdrop.classList.toggle("hidden", !visible);
  document.body.classList.toggle("application-drawer-open", Boolean(visible));
  if (!visible) return;

  const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
  elements.applicationDetailTitle.textContent = role;
  elements.applicationDetailMeta.textContent = [job.location, job.remote_policy]
    .filter(Boolean)
    .join(" / ") || "Location not stated";
  if (job.source_url) {
    elements.applicationSourceLink.href = job.source_url;
    elements.applicationSourceLink.classList.remove("hidden");
  } else {
    elements.applicationSourceLink.href = "#";
    elements.applicationSourceLink.classList.add("hidden");
  }
  const application = state.jobApplication || job.application;

  const currentStatus = application.current_status;
  elements.applicationCurrentStatus.textContent = applicationStatusLabel(currentStatus);
  elements.applicationCurrentStatus.dataset.status = currentStatus;
  elements.applicationSummaryStatus.textContent = applicationStatusLabel(currentStatus);
  elements.applicationStatusSelect.value = currentStatus;
  if (!elements.applicationStatusDate.value) {
    elements.applicationStatusDate.value = todayInputValue();
  }

  elements.applicationStatusSelect.disabled = state.jobApplicationLoading;
  elements.applicationStatusDate.disabled = state.jobApplicationLoading;
  elements.applicationStatusNote.disabled = state.jobApplicationLoading;
  elements.saveApplicationStatusButton.disabled = state.jobApplicationLoading;

  const statusHistory = (application.status_history || []).filter((event) => (
    APPLICATION_STATUS_ORDER.includes(event.status)
  ));
  const appliedEvent = statusHistory.find((event) => event.status === "applied");
  elements.applicationAppliedDate.textContent = appliedEvent
    ? formatApplicationDate(appliedEvent.occurred_at)
    : "Not recorded";

  const assessment = application.assessment
    || (state.selectedJobId === job.id ? state.jobAssessment : null);
  elements.applicationAssessmentSummary.textContent = assessment
    ? assessmentRecommendation(assessment)
    : "Unavailable";
  elements.applicationAssessmentDetail.innerHTML = assessment
    ? '<p class="application-assessment-link-copy">Full findings, evidence, and user resolutions live in Job Workspace.</p>'
    : '<p class="application-assessment-link-copy">Assessment is unavailable. Open Job Workspace to refresh it.</p>';
  renderApplicationSessionPanel(job, application);

  const appliedResumeVersion = application.applied_resume?.recorded
    ? application.applied_resume.resume
    : null;
  const latestSubmittedEvent = application.applied_resume?.history?.at(-1);
  const canOpenSubmittedFile = Boolean(
    appliedResumeVersion?.id && latestSubmittedEvent?.resume?.id === appliedResumeVersion.id
  );
  elements.applicationSubmittedResumeSummary.textContent = appliedResumeVersion?.display_name || "Not recorded";
  elements.applicationSubmittedResumeSummary.title = appliedResumeVersion
    ? "The resume recorded as submitted for this application."
    : ["captured", "reviewed", "preparing"].includes(currentStatus)
      ? "No submitted resume is expected until the application is sent."
      : "The application is recorded, but the submitted resume is still unknown.";
  elements.openSubmittedResumeSummary.classList.toggle("hidden", !canOpenSubmittedFile);

  if (state.jobApplicationLoading && !state.jobApplication) {
    elements.applicationHistoryCount.textContent = "Loading";
    elements.applicationHistoryList.innerHTML = '<p class="empty-state">Loading local status history...</p>';
    return;
  }
  if (state.jobApplicationError) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = state.jobApplicationError;
  }

  const history = (state.jobApplication?.status_history || []).filter((event) => (
    APPLICATION_STATUS_ORDER.includes(event.status)
  ));
  elements.applicationHistoryCount.textContent = `${history.length} event${history.length === 1 ? "" : "s"}`;
  elements.applicationHistoryList.innerHTML = renderApplicationHistory(history);
}

async function openSubmittedResumeFile(event, messageNode) {
  event.preventDefault();
  if (!state.selectedJobId) return;
  // Reserve the preview window during the user gesture, before the authorized fetch.
  const preview = window.open("", "_blank");
  if (!preview) {
    messageNode.classList.add("error");
    messageNode.textContent = "Allow local pop-ups to open the submitted resume.";
    return;
  }
  preview.opener = null;
  try {
    const response = await api.submittedResumeFile(state.selectedJobId);
    const url = URL.createObjectURL(await response.blob());
    preview.location.href = url;
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  } catch (error) {
    preview.close();
    messageNode.classList.add("error");
    messageNode.textContent = formatRequestError(error);
  }
}

async function recordSubmittedResume() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  const resumeId = application?.resume_selection?.resume?.id;
  if (!job?.valid || !resumeId || state.applicationResumeLoading) return;
  state.applicationResumeLoading = true;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = "Recording submitted resume...";
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/submitted-resume`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ resume_id: resumeId }),
      }
    );
    storeJobApplication(application);
    const submitted = application.applied_resume?.resume;
    elements.applicationResumeMessage.textContent = submitted
      ? `Submitted resume recorded: ${submitted.display_name}.`
      : "Submitted resume record updated.";
    toast.show({ message: "Submitted resume recorded in application history." });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function saveLibraryResume() {
  const file = elements.libraryResumeFile.files[0];
  if (state.libraryResumeUploadLoading) return;
  if (!file) {
    elements.libraryResumeMessage.className = "form-message error";
    elements.libraryResumeMessage.textContent = "Choose a resume file first.";
    return;
  }
  state.libraryResumeUploadLoading = true;
  elements.saveLibraryResumeButton.disabled = true;
  elements.saveLibraryResumeButton.textContent = "Saving...";
  elements.libraryResumeMessage.className = "form-message";
  elements.libraryResumeMessage.textContent = `Saving ${file.name} for applications...`;
  try {
    const resume = await api.uploadResumeVersion(file, elements.libraryResumeDisplayName.value.trim());
    invalidateResumeLibraryManagement();
    await loadResumeLibrary();
    elements.libraryResumeFile.value = "";
    elements.libraryResumeDisplayName.value = "";
    elements.libraryResumeMessage.textContent = `Saved ${resume.display_name}. It is now available in Applications.`;
    toast.show({ message: `Resume saved: ${resume.display_name}.` });
  } catch (error) {
    elements.libraryResumeMessage.className = "form-message error";
    elements.libraryResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.libraryResumeUploadLoading = false;
    elements.saveLibraryResumeButton.disabled = false;
    elements.saveLibraryResumeButton.textContent = "Save resume";
  }
}

async function attachApplicationResume() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const file = elements.applicationResumeFile.files[0];
  if (!job?.valid || state.applicationResumeLoading) return;
  if (!file) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = "Choose a resume file first.";
    return;
  }

  state.applicationResumeLoading = true;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = `Adding ${file.name} to your resume library...`;
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/resume`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/octet-stream",
          "X-Resume-Name": encodeURIComponent(file.name),
          "X-Resume-Label": encodeURIComponent(
            elements.applicationResumeDisplayName.value.trim()
          ),
        },
        body: file,
      }
    );
    storeJobApplication(application);
    await loadResumeLibrary();
    elements.applicationResumeFile.value = "";
    elements.applicationResumeDisplayName.value = "";
    const selected = application.resume_selection?.resume;
    elements.applicationResumeMessage.textContent = selected
      ? `Added and selected ${selected.display_name}.`
      : "Resume uploaded.";
    toast.show({
      message: selected
        ? `Resume added: ${selected.display_name}.`
        : "Resume uploaded.",
    });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function loadSelectedJobApplication({ includeSession = true } = {}) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !hasApplicationRecord(job.application)) {
    state.jobApplication = null;
    state.jobApplicationLoading = false;
    state.jobApplicationError = "";
    renderJobApplication();
    renderJobWorkspaceMaterials();
    return;
  }
  const requestedJobId = job.id;
  state.jobApplication = null;
  state.jobApplicationLoading = true;
  state.jobApplicationError = "";
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "";
  renderJobApplication();
  try {
    const [application, sessionPayload] = await Promise.all([
      api.getApplication(requestedJobId),
      includeSession ? api.getApplicationSession(requestedJobId) : Promise.resolve(null),
    ]);
    if (state.selectedJobId !== requestedJobId) return;
    if (sessionPayload) application.application_session = sessionPayload.session || null;
    storeJobApplication(application);
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobApplicationError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobApplicationLoading = false;
      renderJobApplication();
      renderJobWorkspaceMaterials();
    }
  }
}

async function startAttendedApplicationSession() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  if (!job?.valid || !application || state.applicationSessionMutationId) return;
  if (elements.startApplicationSessionButton.dataset.mode === "review") {
    closeApplicationDrawer();
    elements.applicationSessionReviews.scrollIntoView({ behavior: "smooth", block: "start" });
    return;
  }
  if (application.application_session?.state === "armed" && !window.confirm(
    "End the currently armed session and start a fresh one for this same application?"
  )) return;

  state.applicationSessionMutationId = "creating";
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Creating an expiring session for this exact application...";
  renderJobApplication();
  try {
    const result = await api.startApplicationSession(job.id);
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationMessage.className = "form-message success";
    elements.applicationMessage.textContent = "Tracking link ready. Open the posting and choose Track submission in the extension.";
    toast.show({ message: "Application session ready to arm." });
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
  }
}

async function cancelAttendedApplicationSession() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  const session = application?.application_session;
  if (!session || state.applicationSessionMutationId) return;
  if (!window.confirm(
    "Cancel this attended session? The application stage will remain unchanged."
  )) return;
  state.applicationSessionMutationId = session.session_id;
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Cancelling attended session...";
  renderJobApplication();
  try {
    const result = await api.resolveApplicationSession(
      session.session_id,
      "cancel",
      session.revision,
    );
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationMessage.className = "form-message success";
    elements.applicationMessage.textContent = "Session cancelled. Application status was not changed.";
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
  }
}

function markResumePlanDirty() {
  state.resumePlanDirty = true;
  elements.resumePlanStatus.textContent = "Unsaved changes";
  elements.resumePlanIssueCount.textContent = "Save to validate";
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = "Selections changed locally.";
}

async function loadResumePlan({ force = false } = {}) {
  const jobId = state.resumePlanJobId;
  if (!jobId || state.resumePlanLoading) return;
  if (!force && state.resumePlanEditor?.plan?.job_id === jobId) return;
  state.resumePlanLoading = true;
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = "Loading Career Vault selections...";
  renderResumePlan();
  try {
    const editor = await requestJson(`/api/jobs/${encodeURIComponent(jobId)}/resume-plan`);
    if (state.resumePlanJobId !== jobId) return;
    state.resumePlanEditor = editor;
    state.resumePlanDirty = false;
    elements.resumePlanWorkspace.open = editor.plan?.status !== "approved";
    elements.resumePlanMessage.textContent = editor.persisted
      ? "Saved content selection loaded."
      : "Choose only the evidence that belongs in this resume.";
  } catch (error) {
    if (state.resumePlanJobId !== jobId) return;
    state.resumePlanEditor = null;
    elements.resumePlanMessage.className = "form-message error";
    elements.resumePlanMessage.textContent = formatRequestError(error);
  } finally {
    if (state.resumePlanJobId === jobId) {
      state.resumePlanLoading = false;
      renderResumePlan();
    }
  }
}

function renderResumePlan() {
  const editor = state.resumePlanEditor;
  const job = state.jobs.find((item) => item.id === state.resumePlanJobId);
  setResumeMaterialStep(state.resumeMaterialStep);
  elements.resumePlanTitle.textContent = "Choose what belongs in this resume";
  elements.resumePlanMeta.textContent = job
    ? [job.company, job.title].filter(Boolean).join(" / ") || "Career Vault evidence"
    : "Career Vault remains unchanged.";
  const disabled = state.resumePlanLoading || !editor;
  elements.resumePlanPages.disabled = disabled;
  elements.saveResumePlanButton.disabled = disabled || !state.resumePlanDirty;
  elements.approveResumePlanButton.disabled = disabled;
  if (!editor) {
    elements.resumePlanStatus.textContent = state.resumePlanLoading ? "Loading" : "Unavailable";
    elements.resumePlanRevision.textContent = "Not loaded";
    elements.resumePlanIssueCount.textContent = state.resumePlanLoading ? "Loading" : "Unavailable";
    elements.resumePlanIssues.innerHTML = "";
    elements.resumePlanSectionList.innerHTML = "";
    elements.resumePlanExperienceList.innerHTML = "";
    elements.resumePlanProjectSection.hidden = true;
    elements.resumePlanProjectList.innerHTML = "";
    elements.resumePlanSkillList.innerHTML = "";
    elements.resumePlanEducationList.innerHTML = "";
    return;
  }

  const plan = editor.plan;
  const catalog = editor.catalog;
  elements.resumePlanPages.value = String(plan.target_pages);
  elements.resumePlanStatus.textContent = state.resumePlanDirty
    ? "Editing"
    : plan.status === "approved" ? "Selected" : "In progress";
  elements.resumePlanRevision.textContent = editor.persisted
    ? `Revision ${editor.revision}`
    : "Not saved";
  const issues = editor.validation.issues || [];
  elements.resumePlanIssueCount.textContent = state.resumePlanDirty
    ? "Selection changed"
    : editor.validation.ready_for_approval ? "Ready to use" : `${issues.length} to resolve`;
  elements.resumePlanValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_for_approval && !state.resumePlanDirty
  );
  elements.resumePlanIssues.innerHTML = renderResumePlanIssues(issues);

  elements.resumePlanSectionList.innerHTML = renderResumePlanSections(plan.sections);

  const experienceView = renderResumePlanExperiences({
    catalog,
    plan,
    attentionBlockId: state.resumePlanAttentionBlockId,
  });
  elements.resumePlanExperienceCount.textContent = experienceView.count;
  elements.resumePlanExperienceList.innerHTML = experienceView.html;

  const projectsEnabled = plan.sections.some((section) => (
    section.key === "projects" && section.included
  ));
  elements.resumePlanProjectSection.hidden = !projectsEnabled;
  const projectView = renderResumePlanProjects({
    catalog,
    plan,
    enabled: projectsEnabled,
  });
  elements.resumePlanProjectCount.textContent = projectView.count;
  elements.resumePlanProjectList.innerHTML = projectView.html;

  const skillView = renderResumePlanSkills({ catalog, plan });
  elements.resumePlanSkillCount.textContent = skillView.count;
  elements.resumePlanSkillList.innerHTML = skillView.html;

  const educationView = renderResumePlanEducation({ catalog, plan });
  elements.resumePlanEducationCount.textContent = educationView.count;
  elements.resumePlanEducationList.innerHTML = educationView.html;

}

function findExperiencePlan(blockId) {
  return state.resumePlanEditor?.plan.experiences.find((item) => item.block_id === blockId);
}

function updateStringSelection(items, value, selected) {
  const values = new Set(items);
  if (selected) values.add(value);
  else values.delete(value);
  return [...values];
}

function setResumePlanExperienceDecision(button) {
  const item = findExperiencePlan(button.dataset.planExperienceDecision);
  if (!item) return;
  const decision = button.dataset.planDecisionValue === "include" ? "include" : "exclude";
  item.decision = decision;
  if (decision === "include") {
    item.target_bullets = item.target_bullets || 2;
    item.presentation = item.target_bullets === 2 ? "compact" : "full";
    item.reason = "";
  } else {
    item.presentation = null;
    item.target_bullets = null;
    item.proof_ids = [];
  }
  markResumePlanDirty();
  renderResumePlan();
}

function handleResumePlanChange(event) {
  const editor = state.resumePlanEditor;
  if (!editor) return;
  const target = event.target;
  let rerender = false;
  if (target === elements.resumePlanPages) {
    editor.plan.target_pages = Number(target.value);
  } else if (target.dataset.planSection) {
    const section = editor.plan.sections.find((item) => item.key === target.dataset.planSection);
    if (section) {
      section.included = target.checked;
      if (section.key === "projects" && !section.included) {
        editor.plan.projects = [];
      }
    }
    rerender = true;
  } else if (target.dataset.planExperienceTarget) {
    const item = findExperiencePlan(target.dataset.planExperienceTarget);
    if (item) {
      item.target_bullets = Number(target.value);
      item.presentation = item.target_bullets === 2 ? "compact" : "full";
    }
    rerender = true;
  } else if (target.dataset.planExperienceReason) {
    const item = findExperiencePlan(target.dataset.planExperienceReason);
    if (item) item.reason = target.value;
  } else if (target.dataset.planExperienceProof) {
    const item = findExperiencePlan(target.dataset.planExperienceProof);
    if (item) item.proof_ids = updateStringSelection(item.proof_ids, target.value, target.checked);
    rerender = true;
  } else if (target.dataset.planProject) {
    const blockId = target.dataset.planProject;
    const projectsEnabled = editor.plan.sections.some((section) => (
      section.key === "projects" && section.included
    ));
    if (!projectsEnabled) return;
    if (target.checked) editor.plan.projects.push({ block_id: blockId, proof_ids: [], reason: "" });
    else editor.plan.projects = editor.plan.projects.filter((item) => item.block_id !== blockId);
    rerender = true;
  } else if (target.dataset.planProjectProof) {
    const item = editor.plan.projects.find((project) => project.block_id === target.dataset.planProjectProof);
    if (item) item.proof_ids = updateStringSelection(item.proof_ids, target.value, target.checked);
  } else if (target.dataset.planSkill) {
    const blockId = target.dataset.planSkill;
    if (target.checked) editor.plan.skills.push({ block_id: blockId, keywords: [] });
    else editor.plan.skills = editor.plan.skills.filter((item) => item.block_id !== blockId);
    rerender = true;
  } else if (target.dataset.planSkillKeyword) {
    const item = editor.plan.skills.find((skill) => skill.block_id === target.dataset.planSkillKeyword);
    if (item) item.keywords = updateStringSelection(item.keywords, target.value, target.checked);
  } else if (target.dataset.planEducation) {
    const blockId = target.dataset.planEducation;
    const recordType = target.dataset.planEducationType;
    if (target.checked) editor.plan.education.push({ block_id: blockId, record_type: recordType });
    else editor.plan.education = editor.plan.education.filter((item) => !(item.block_id === blockId && item.record_type === recordType));
    rerender = true;
  } else {
    return;
  }
  markResumePlanDirty();
  if (rerender) renderResumePlan();
}
function moveResumePlanSection(button) {
  const sections = state.resumePlanEditor?.plan.sections;
  if (!sections) return;
  const index = sections.findIndex((item) => item.key === button.dataset.planSectionKey);
  const destination = button.dataset.planSectionMove === "up" ? index - 1 : index + 1;
  if (index < 0 || destination < 0 || destination >= sections.length) return;
  [sections[index], sections[destination]] = [sections[destination], sections[index]];
  markResumePlanDirty();
  renderResumePlan();
}

function focusResumePlanIssue(issue) {
  if (!issue?.blockId) return false;
  state.resumePlanAttentionBlockId = issue.blockId;
  elements.resumePlanMessage.className = "form-message error";
  elements.resumePlanMessage.textContent = issue.message;
  renderResumePlan();
  window.requestAnimationFrame(() => {
    const card = [...elements.resumePlanExperienceList.querySelectorAll("[data-plan-experience-card]")]
      .find((candidate) => candidate.dataset.planExperienceCard === issue.blockId);
    if (!card) return;
    card.scrollIntoView({ behavior: "smooth", block: "center" });
    const target = card.querySelector(`input[data-plan-experience-proof]:not(:checked)`)
      || card.querySelector(`select[data-plan-experience-target]`)
      || card;
    target.focus({ preventScroll: true });
  });
  return true;
}

function focusResumePlanIssueFromError(error) {
  const message = error?.payload?.technical_message || error?.message || "";
  const source = state.resumePlanEditor?.catalog?.experiences?.find((item) => (
    message.includes(`${item.company} / ${item.role}`)
  ));
  return source ? focusResumePlanIssue({ blockId: source.id, message }) : false;
}
async function saveCurrentResumePlan(approve) {
  const editor = state.resumePlanEditor;
  const jobId = state.resumePlanJobId;
  if (!editor || !jobId || state.resumePlanLoading) return;
  if (approve) {
    const localIssue = firstResumePlanApprovalIssue(editor);
    if (localIssue) {
      focusResumePlanIssue(localIssue);
      return;
    }
  }
  state.resumePlanAttentionBlockId = null;
  state.resumePlanLoading = true;
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = approve
    ? "Checking the selected content..."
    : "Saving selection progress...";
  editor.plan.vault_revision = editor.catalog.vault_revision;
  editor.plan.job_fingerprint = editor.current_job_fingerprint;
  renderResumePlan();
  try {
    const saved = await requestJson(`/api/jobs/${encodeURIComponent(jobId)}/resume-plan`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        action: approve ? "approve" : "save_draft",
        expected_revision: editor.revision,
        plan: editor.plan,
      }),
    });
    state.resumePlanEditor = saved;
    state.resumePlanDirty = false;
    const job = state.jobs.find((item) => item.id === jobId);
    if (job?.application) {
      job.application.resume_plan = {
        status: saved.plan.status,
        revision: saved.revision,
        updated_at: new Date().toISOString(),
      };
    }
    elements.resumePlanMessage.textContent = approve
      ? "Content selected. The tailored wording is ready below."
      : "Selection progress saved.";
    toast.show({ message: approve ? "Resume content selected." : "Selection progress saved." });
  } catch (error) {
    if (!focusResumePlanIssueFromError(error)) {
      elements.resumePlanMessage.className = "form-message error";
      elements.resumePlanMessage.textContent = formatRequestError(error);
    }
  } finally {
    state.resumePlanLoading = false;
    renderResumePlan();
  }
  if (approve && state.resumePlanEditor?.plan?.status === "approved") {
    elements.resumePlanWorkspace.open = false;
    state.resumeMaterialEditor = null;
    await loadResumeMaterial({ force: true });
    setResumeMaterialStep("edit");
  }
}

function materialJob() {
  return state.jobs.find((item) => item.id === state.materialEditorJobId);
}

function setMaterialTab(tab) {
  state.materialEditorTab = tab === "resume" ? "resume" : "cover-letter";
  elements.materialTabButtons.forEach((button) => {
    const active = button.dataset.materialTab === state.materialEditorTab;
    button.setAttribute("aria-selected", active ? "true" : "false");
    button.tabIndex = active ? 0 : -1;
    button.classList.toggle("is-active", active);
  });
  elements.materialResumePanel.hidden = state.materialEditorTab !== "resume";
  elements.materialCoverLetterPanel.hidden = state.materialEditorTab !== "cover-letter";
  if (state.materialEditorTab === "resume") setResumeMaterialStep(state.resumeMaterialStep);
}

function setResumeMaterialStep(step, { focus = false } = {}) {
  const steps = ["select", "edit", "review"];
  const planApproved = state.resumePlanEditor?.plan?.status === "approved";
  const busy = state.resumePlanLoading || state.resumeMaterialLoading;
  const requestedStep = steps.includes(step) ? step : "select";
  const nextStep = requestedStep !== "select" && !planApproved ? "select" : requestedStep;
  state.resumeMaterialStep = nextStep;
  elements.resumeMaterialStepButtons.forEach((button) => {
    const active = button.dataset.resumeMaterialStep === nextStep;
    const unavailable = button.dataset.resumeMaterialStep !== "select" && !planApproved;
    if (active) button.setAttribute("aria-current", "step");
    else button.removeAttribute("aria-current");
    button.disabled = unavailable || busy;
    button.setAttribute("aria-disabled", String(unavailable || busy));
    button.classList.toggle("is-active", active);
    if (active && focus) button.focus();
  });
  const sections = {
    select: [elements.resumePlanWorkspace, elements.materialResumeContext],
    edit: [
      elements.materialResumeContext,
      elements.resumeMaterialToolbar,
      elements.resumeMaterialValidation,
      elements.resumeMaterialForm,
    ],
    review: [
      elements.resumeMaterialValidation,
      elements.resumeMaterialPreview,
      elements.resumeMaterialExportBar,
      elements.resumeMaterialScopeNote,
    ],
  };
  const managed = new Set(Object.values(sections).flat().filter(Boolean));
  managed.forEach((node) => {
    node.classList.toggle("resume-material-step-hidden", !sections[nextStep].includes(node));
  });
  elements.resumeMaterialStepBack.disabled = busy || nextStep === "select";
  elements.resumeMaterialStepNext.disabled = busy
    || (nextStep === "select" ? !state.resumePlanEditor : !state.resumeMaterialEditor);
  elements.resumeMaterialStepNext.textContent = nextStep === "select"
    ? (state.resumePlanEditor?.plan?.status === "approved" ? "Continue" : "Use selection")
    : nextStep === "edit" ? "Review" : "Done";
  if (nextStep === "select") elements.resumePlanWorkspace.open = true;
  if (nextStep === "review") elements.resumeMaterialPreview.open = true;
}

async function advanceResumeMaterialStep() {
  if (state.resumeMaterialStep === "select") {
    if (state.resumePlanEditor?.plan?.status !== "approved") {
      await saveCurrentResumePlan(true);
      if (state.resumePlanEditor?.plan?.status !== "approved") return;
    }
    setResumeMaterialStep("edit", { focus: true });
    return;
  }
  if (state.resumeMaterialStep === "edit") {
    if (state.resumeMaterialDirty) {
      await saveResumeMaterial(false);
      if (state.resumeMaterialDirty) return;
    }
    setResumeMaterialStep("review", { focus: true });
    return;
  }
  returnFromMaterialEditor();
}

async function retreatResumeMaterialStep() {
  const prior = state.resumeMaterialStep === "review" ? "edit" : "select";
  await activateResumeMaterialStep(prior, { focus: true });
}

async function activateResumeMaterialStep(step, { focus = false } = {}) {
  if (step !== state.resumeMaterialStep && state.resumeMaterialStep === "edit" && state.resumeMaterialDirty) {
    await saveResumeMaterial(false);
    if (state.resumeMaterialDirty) return;
  }
  setResumeMaterialStep(step, { focus });
}

async function activateMaterialTab(tab) {
  setMaterialTab(tab);
  renderApplicationMaterials();
  if (state.materialEditorTab === "resume") {
    state.resumePlanJobId = state.materialEditorJobId;
    await Promise.all([loadResumePlan(), loadResumeMaterial()]);
  } else {
    await loadCoverLetterMaterial();
  }
}

async function openApplicationMaterials(tab = "cover-letter") {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !canManageApplicationMaterials(job.application)) return;
  state.materialEditorReturnView = state.activeView === "applications"
    ? "applications"
    : "job-library";
  state.materialEditorJobId = job.id;
  state.materialEditor = null;
  state.materialEditorSaving = false;
  state.materialEditorDirty = false;
  state.resumeMaterialEditor = null;
  state.resumeMaterialDirty = false;
  state.resumePlanJobId = job.id;
  state.resumePlanEditor = null;
  state.resumePlanDirty = false;
  state.resumeMaterialStep = "select";
  state.materialEvidenceSearch = "";
  setMaterialTab(tab);
  if (state.applicationDrawerOpen) closeApplicationDrawer();
  switchView("application-materials");
  if (state.materialEditorTab === "resume") {
    await Promise.all([loadResumePlan(), loadResumeMaterial()]);
  } else {
    await loadCoverLetterMaterial();
  }
}

async function loadCoverLetterMaterial({ force = false } = {}) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.materialEditorLoading) return;
  if (!force && state.materialEditor?.draft?.job_id === jobId) return;
  state.materialEditorLoading = true;
  state.materialEditorDirty = false;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Loading application material...";
  renderApplicationMaterials();
  try {
    const editor = await api.getCoverLetter(jobId);
    if (state.materialEditorJobId !== jobId) return;
    state.materialEditor = editor;
    state.coverLetterSummaries[jobId] = coverLetterSummary(editor);
    elements.materialEditorMessage.textContent = editor.persisted
      ? "Saved draft loaded."
      : "New cover letter started from the standard template and current source facts.";
  } catch (error) {
    if (state.materialEditorJobId !== jobId) return;
    state.materialEditor = null;
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    if (state.materialEditorJobId === jobId) {
      state.materialEditorLoading = false;
      renderApplicationMaterials();
    }
  }
}

async function loadResumeMaterial({ force = false } = {}) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.resumeMaterialLoading) return;
  if (!force && state.resumeMaterialEditor?.draft?.job_id === jobId) return;
  state.resumeMaterialLoading = true;
  state.resumeMaterialDirty = false;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Loading application resume...";
  renderApplicationMaterials();
  try {
    const editor = await api.getResumeMaterial(jobId);
    if (state.materialEditorJobId !== jobId) return;
    state.resumeMaterialEditor = editor;
    elements.materialEditorMessage.textContent = editor.persisted
      ? "Saved application resume loaded."
      : "New application resume built from the current content selection.";
  } catch (error) {
    if (state.materialEditorJobId !== jobId) return;
    state.resumeMaterialEditor = null;
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    if (state.materialEditorJobId === jobId) {
      state.resumeMaterialLoading = false;
      renderApplicationMaterials();
    }
  }
}

function selectedCoverLetterEvidenceRefs() {
  return (state.materialEditor?.draft?.content?.evidence_items || [])
    .flatMap((item) => item.evidence_refs || []);
}

function renderCoverLetterCatalog() {
  const editor = state.materialEditor;
  const expandedSourceKeys = [
    ...(elements.coverLetterEvidenceCatalog?.querySelectorAll(
      ".material-evidence-source-group[open]",
    ) || []),
  ].map((group) => group.dataset.evidenceSourceKey).filter(Boolean);
  elements.coverLetterEvidenceCatalog.innerHTML = renderCoverLetterEvidenceCatalog(
    editor?.catalog?.evidence || [],
    state.materialEvidenceSearch,
    selectedCoverLetterEvidenceRefs(),
    expandedSourceKeys,
  );
}

function writeCoverLetterForm(draft) {
  elements.coverLetterName.value = draft.candidate.name || "";
  elements.coverLetterEmail.value = draft.candidate.email || "";
  elements.coverLetterLocation.value = draft.candidate.location_line || "";
  elements.coverLetterPhone.value = draft.candidate.phone || "";
  elements.coverLetterLinkedin.value = draft.candidate.linkedin || "";
  elements.coverLetterGithub.value = draft.candidate.github || "";
  elements.coverLetterWebsite.value = draft.candidate.website || "";
  elements.coverLetterDate.value = draft.content.letter_date || "";
  elements.coverLetterCompany.value = draft.company || "";
  elements.coverLetterRole.value = draft.role_title || "";
  elements.coverLetterSalutation.value = draft.content.salutation || "";
  elements.coverLetterOpening.value = draft.content.opening || "";
  elements.coverLetterLeadIn.value = draft.content.evidence_lead_in || "";
  elements.coverLetterClosing.value = draft.content.closing || "";
  elements.coverLetterSignOff.value = draft.content.sign_off || "";
  elements.coverLetterSignature.value = draft.content.signature_name || "";
  elements.coverLetterCredential.value = draft.content.credential_line || "";
}

function coverLetterHasStaleSources(editor) {
  return (editor?.validation?.issues || []).some((issue) => (
    ["MATERIAL_JOB_STALE", "MATERIAL_PROFILE_STALE", "MATERIAL_EVIDENCE_STALE"].includes(issue.code)
  ));
}

function writeResumeMaterialForm(draft) {
  elements.resumeMaterialName.value = draft.candidate.name || "";
  elements.resumeMaterialEmail.value = draft.candidate.email || "";
  elements.resumeMaterialLocation.value = draft.candidate.location_line || "";
  elements.resumeMaterialPhone.value = draft.candidate.phone || "";
  elements.resumeMaterialLinkedin.value = draft.candidate.linkedin || "";
  elements.resumeMaterialGithub.value = draft.candidate.github || "";
  elements.resumeMaterialWebsite.value = draft.candidate.website || "";
  elements.resumeMaterialSummary.value = draft.summary || "";
}

function resumeMaterialHasStalePlan(editor) {
  return (editor?.validation?.issues || []).some((issue) => [
    "RESUME_JOB_STALE",
    "RESUME_PROFILE_STALE",
    "RESUME_EVIDENCE_STALE",
    "RESUME_PLAN_STALE",
    "RESUME_PLAN_CONTENT_STALE",
  ].includes(issue.code));
}

function renderResumeMaterialPanel() {
  renderResumePlan();
  const editor = state.resumeMaterialEditor;
  const disabled = state.resumeMaterialLoading || !editor;
  elements.resumeMaterialForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  elements.saveResumeMaterialButton.disabled = disabled || !state.resumeMaterialDirty;
  elements.approveResumeMaterialButton.disabled = disabled
    || state.resumeMaterialDirty
    || !editor?.validation?.ready_to_export;
  elements.copyResumeAiCommand.disabled = disabled;
  if (!editor) {
    elements.resumeMaterialIssueCount.textContent = state.resumeMaterialLoading
      ? "Loading"
      : "Unavailable";
    elements.resumeMaterialIssues.innerHTML = "";
    elements.resumeMaterialExperiences.innerHTML = "";
    elements.resumeMaterialProjects.innerHTML = "";
    elements.resumeMaterialSkills.innerHTML = "";
    elements.resumeMaterialEducation.innerHTML = "";
    elements.resumeMaterialSummaryEvidence.innerHTML = "";
    elements.resumeMaterialPreviewText.textContent = "";
    elements.rebuildResumeMaterialButton.classList.add("hidden");
    elements.resumeMaterialExportButtons.forEach((button) => { button.disabled = true; });
    if (state.materialEditorTab === "resume") {
      elements.materialEditorStatus.textContent = state.resumeMaterialLoading ? "Loading" : "Unavailable";
      elements.materialEditorRevision.textContent = "Not loaded";
    }
    return;
  }

  const draft = editor.draft;
  elements.resumeMaterialIssueCount.textContent = materialIssueSummary(
    editor.validation,
    state.resumeMaterialDirty,
  );
  elements.resumeMaterialValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_to_export && !state.resumeMaterialDirty,
  );
  elements.resumeMaterialIssues.innerHTML = renderMaterialIssues(editor.validation, {
    dirty: state.resumeMaterialDirty,
  });
  elements.rebuildResumeMaterialButton.classList.toggle(
    "hidden",
    !resumeMaterialHasStalePlan(editor),
  );
  elements.rebuildResumeMaterialButton.disabled = state.resumeMaterialLoading;
  writeResumeMaterialForm(draft);
  const rendered = renderResumeBlocks(draft, editor.catalog?.evidence || []);
  elements.resumeMaterialExperiences.innerHTML = rendered.experience;
  elements.resumeMaterialProjects.innerHTML = rendered.projects;
  elements.resumeMaterialSkills.innerHTML = renderResumeSkills(draft.skills);
  elements.resumeMaterialEducation.innerHTML = renderResumeEducation(draft.education);
  elements.resumeMaterialForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  const evidenceById = new Map(
    (editor.catalog?.evidence || []).map((item) => [item.id, item])
  );
  elements.resumeMaterialSummaryEvidence.innerHTML = renderResumeEvidenceRefs(
    draft.summary_evidence_refs,
    evidenceById,
  ) || '<span class="empty-state">No evidence attached.</span>';
  elements.resumeMaterialSummarySection.hidden = !draft.sections.includes("professional_summary");
  elements.resumeMaterialProjectsSection.hidden = !draft.sections.includes("projects");
  elements.resumeMaterialPreviewText.textContent = editor.preview?.plain_text || "";
  const exportReady = Boolean(
    editor.persisted
    && editor.validation.ready_to_export
    && !state.resumeMaterialDirty
    && !state.resumeMaterialLoading
  );
  elements.resumeMaterialExportNote.textContent = exportReady
    ? `${editor.preview.word_count} words / ATS structure and evidence checks passed.`
    : "Use a valid content selection and save the application-only wording before exporting.";
  elements.resumeMaterialExportButtons.forEach((button) => {
    button.disabled = !exportReady;
  });
  if (state.materialEditorTab === "resume") {
    elements.materialEditorStatus.textContent = state.resumeMaterialDirty
      ? "Unsaved changes"
      : draft.status;
    elements.materialEditorRevision.textContent = editor.persisted
      ? `Revision ${editor.revision}`
      : "Not saved";
  }
}

function renderApplicationMaterials() {
  syncMaterialReviews();
  const job = materialJob();
  const editor = state.materialEditor;
  elements.materialEditorTitle.textContent = job
    ? [job.company, job.title].filter(Boolean).join(" / ") || "Application materials"
    : "Application materials";
  elements.materialEditorMeta.textContent = job
    ? [job.location, job.remote_policy].filter(Boolean).join(" / ") || "Location not stated"
    : "Select a prepared application.";
  elements.materialEditorBackButton.textContent = state.materialEditorReturnView === "applications"
    ? "← Applications"
    : "← Job Workspace";
  setMaterialTab(state.materialEditorTab);

  const selectedResume = job?.application?.resume_selection?.resume;
  elements.materialSelectedResumeName.textContent = selectedResume?.display_name || "No resume selected";
  elements.materialSelectedResumeMeta.textContent = selectedResume
    ? "This immutable Resume Version is selected for tailoring."
    : "Select or upload an immutable Resume Version in Job Workspace.";

  renderResumeMaterialPanel();

  const disabled = state.materialEditorLoading || !editor;
  elements.coverLetterForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  elements.copyCoverLetterAiCommand.disabled = disabled;
  elements.addCoverLetterEvidenceButton.disabled = disabled
    || (editor?.draft?.content?.evidence_items || []).length >= 4;
  if (!editor) {
    if (state.materialEditorTab === "cover-letter") {
      elements.materialEditorStatus.textContent = state.materialEditorLoading ? "Loading" : "Unavailable";
      elements.materialEditorRevision.textContent = "Not loaded";
    }
    elements.coverLetterIssueCount.textContent = state.materialEditorLoading ? "Loading" : "Unavailable";
    elements.coverLetterIssues.innerHTML = "";
    elements.coverLetterEvidenceCatalog.innerHTML = "";
    elements.coverLetterEvidenceItems.innerHTML = "";
    elements.coverLetterPreviewText.textContent = "";
    elements.refreshCoverLetterSources.classList.add("hidden");
    elements.coverLetterSaveState.textContent = state.materialEditorLoading
      ? "Loading..."
      : "Unavailable";
    elements.coverLetterExportButtons.forEach((button) => { button.disabled = true; });
    updateMaterialReviewControls();
    return;
  }

  const draft = editor.draft;
  if (state.materialEditorTab === "cover-letter") {
    elements.materialEditorStatus.textContent = state.materialEditorDirty
      ? "Editing"
      : editor.validation.ready_to_export ? "Ready" : "Needs review";
    elements.materialEditorRevision.textContent = editor.persisted
      ? `Saved revision ${editor.revision}`
      : "Not saved";
  }
  elements.coverLetterSaveState.textContent = state.materialEditorDirty
    ? "Current changes will be saved when you export."
    : editor.persisted
      ? `Saved locally / revision ${editor.revision}`
      : "Export saves and validates automatically.";
  elements.coverLetterIssueCount.textContent = coverLetterIssueSummary(
    editor.validation,
    state.materialEditorDirty,
  );
  elements.coverLetterValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_to_export && !state.materialEditorDirty,
  );
  elements.coverLetterIssues.innerHTML = renderMaterialIssues(editor.validation, {
    dirty: state.materialEditorDirty,
  });
  elements.refreshCoverLetterSources.classList.toggle(
    "hidden",
    !coverLetterHasStaleSources(editor),
  );
  elements.refreshCoverLetterSources.disabled = state.materialEditorLoading;
  writeCoverLetterForm(draft);
  elements.coverLetterEvidenceItems.innerHTML = renderCoverLetterEvidenceItems(
    draft.content.evidence_items,
    editor.catalog.evidence,
  );
  renderCoverLetterCatalog();
  elements.coverLetterPreviewText.textContent = editor.preview?.plain_text || "";

  const exportReady = Boolean(editor.validation.ready_to_export && !state.materialEditorDirty);
  elements.coverLetterExportNote.textContent = exportReady
    ? `${editor.preview.word_count} words / structure and source references passed.`
    : state.materialEditorDirty || !editor.persisted
      ? "Export saves the current letter, reruns checks, and then creates the file."
      : "Resolve the readiness items above; Export will recheck automatically.";
  elements.coverLetterExportButtons.forEach((button) => {
    button.disabled = disabled;
  });
  updateMaterialReviewControls();
}

function syncCoverLetterSimpleField(target) {
  const draft = state.materialEditor?.draft;
  if (!draft) return false;
  const bindings = new Map([
    [elements.coverLetterName, [draft.candidate, "name"]],
    [elements.coverLetterEmail, [draft.candidate, "email"]],
    [elements.coverLetterLocation, [draft.candidate, "location_line"]],
    [elements.coverLetterPhone, [draft.candidate, "phone"]],
    [elements.coverLetterLinkedin, [draft.candidate, "linkedin"]],
    [elements.coverLetterGithub, [draft.candidate, "github"]],
    [elements.coverLetterWebsite, [draft.candidate, "website"]],
    [elements.coverLetterDate, [draft.content, "letter_date"]],
    [elements.coverLetterCompany, [draft, "company"]],
    [elements.coverLetterRole, [draft, "role_title"]],
    [elements.coverLetterSalutation, [draft.content, "salutation"]],
    [elements.coverLetterOpening, [draft.content, "opening"]],
    [elements.coverLetterLeadIn, [draft.content, "evidence_lead_in"]],
    [elements.coverLetterClosing, [draft.content, "closing"]],
    [elements.coverLetterSignOff, [draft.content, "sign_off"]],
    [elements.coverLetterSignature, [draft.content, "signature_name"]],
    [elements.coverLetterCredential, [draft.content, "credential_line"]],
  ]);
  const binding = bindings.get(target);
  if (!binding) return false;
  binding[0][binding[1]] = target.value;
  return true;
}

function coverLetterLines(value) {
  return [...new Set(String(value || "").split(/\r?\n/).map((item) => item.trim()).filter(Boolean))];
}

function handleCoverLetterInput(event) {
  const editor = state.materialEditor;
  if (!editor || state.materialEditorLoading) return;
  const target = event.target;
  let changed = syncCoverLetterSimpleField(target);
  const itemField = target.dataset.coverLetterItemField;
  if (itemField) {
    const card = target.closest("[data-cover-letter-item-index]");
    const item = editor.draft.content.evidence_items[Number(card?.dataset.coverLetterItemIndex)];
    if (item) {
      item[itemField] = itemField === "manual_checks"
        ? coverLetterLines(target.value)
        : target.value;
      changed = true;
    }
  }
  if (!changed) return;
  state.materialEditorDirty = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Editing locally. Export will save and validate the current letter.";
  syncMaterialReviews();
  updateMaterialReviewControls();
  elements.materialEditorStatus.textContent = "Editing";
  elements.coverLetterIssueCount.textContent = "Changes not checked";
  elements.coverLetterSaveState.textContent = "Current changes will be saved when you export.";
  elements.coverLetterExportNote.textContent = "Export saves the current letter, reruns checks, and then creates the file.";
}

function newCoverLetterItem({ evidence = null } = {}) {
  return {
    id: `point_${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`,
    label: evidence?.source_type === "project" ? "Applied project work" : "Relevant experience",
    claim: evidence?.claim || "",
    evidence_refs: evidence ? [evidence.id] : [],
    manual_checks: evidence ? [] : ["Verify this claim before export."],
    origin: "user",
  };
}

function addCoverLetterEvidence(reference = null) {
  const editor = state.materialEditor;
  const items = editor?.draft?.content?.evidence_items;
  if (!items || items.length >= 4) return;
  const evidence = reference
    ? editor.catalog.evidence.find((item) => item.id === reference)
    : null;
  if (reference && !evidence) return;
  items.push(newCoverLetterItem({ evidence }));
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = evidence
    ? "Evidence added. Tailor the label and claim without overstating the source."
    : "Manual point added. Resolve its verification check before export.";
}

function removeCoverLetterEvidence(index) {
  const items = state.materialEditor?.draft?.content?.evidence_items;
  if (!items?.[index]) return;
  items.splice(index, 1);
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Evidence point removed locally.";
}

function refreshCoverLetterSources() {
  const editor = state.materialEditor;
  if (!editor) return;
  const defaults = editor.source_defaults;
  const current = editor.current_sources;
  editor.draft.job_fingerprint = current.job_fingerprint;
  editor.draft.profile_revision = current.profile_revision;
  editor.draft.evidence_revision = current.evidence_revision;
  editor.draft.company = defaults.company;
  editor.draft.role_title = defaults.role_title;
  editor.draft.candidate = structuredClone(defaults.candidate);
  editor.draft.content.salutation = defaults.content.salutation;
  editor.draft.content.signature_name = defaults.content.signature_name;
  editor.draft.content.credential_line = defaults.content.credential_line;
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Current job/profile facts refreshed. Review the prose, then export.";
}

async function persistCoverLetterForExport() {
  const editor = state.materialEditor;
  const jobId = state.materialEditorJobId;
  if (!editor || !jobId || state.materialEditorLoading || state.materialEditorSaving) return false;
  if (!elements.coverLetterForm.reportValidity()) return false;
  if (editor.persisted && !state.materialEditorDirty) {
    const ready = Boolean(editor.validation?.ready_to_export);
    if (!ready) {
      elements.materialEditorMessage.className = "form-message error";
      elements.materialEditorMessage.textContent = "Resolve the readiness items above before export.";
    }
    return ready;
  }
  state.materialEditorSaving = true;
  state.materialEditorLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Saving and checking the current cover letter...";
  renderApplicationMaterials();
  try {
    const saved = await api.saveCoverLetter(jobId, {
      action: "save_draft",
      expectedRevision: editor.revision,
      draft: editor.draft,
    });
    state.materialEditor = saved;
    state.materialEditorDirty = false;
    state.coverLetterSummaries[jobId] = coverLetterSummary(saved);
    if (!saved.validation?.ready_to_export) {
      elements.materialEditorMessage.className = "form-message error";
      elements.materialEditorMessage.textContent = "Saved locally. Resolve the readiness items before export.";
      return false;
    }
    elements.materialEditorMessage.textContent = "Draft saved. Checking export readiness...";
    return true;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
    return false;
  } finally {
    state.materialEditorSaving = false;
    state.materialEditorLoading = false;
    renderApplicationMaterials();
  }
}

async function downloadCoverLetter(format) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.materialEditorLoading) return;
  const ready = await persistCoverLetterForExport();
  if (!ready) {
    renderApplicationMaterials();
    return;
  }
  if (!await materialReviews.cover_letter.ensureReady() || state.materialEditorJobId !== jobId || state.materialEditorDirty) return;
  state.materialEditorLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = `Rendering and validating ${format.toUpperCase()}...`;
  renderApplicationMaterials();
  try {
    const { blob, filename } = await api.downloadCoverLetter(jobId, format);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.materialEditorMessage.textContent = `${filename} passed structural checks and was downloaded.`;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.materialEditorLoading = false;
    renderApplicationMaterials();
  }
}

// Build-with-AI commands target the verified career-hub-agent client. In the
// desktop app the command is complete (verified executable and current server);
// in plain browser mode it uses the $agent/$connection placeholders from the skills.
async function buildAgentMaterialCommand(jobId, kind) {
  const args = `material-context --job-id ${jobId} --kind ${kind} --pretty`;
  const bridge = window.careerHubDesktop?.agent;
  if (bridge?.getConnection) {
    try {
      const connection = await bridge.getConnection();
      if (connection?.available && connection.executablePath && connection.connection) {
        // The connection file path, never a token (ADR SEC-250).
        return { command: `& "${connection.executablePath}" ${args.replace("material-context", `material-context --connection "${connection.connection}"`)}`, complete: true };
      }
    } catch (_error) {
      // Fall back to the placeholder form below.
    }
  }
  return { command: `& $agent ${args.replace("material-context", "material-context --connection $connection")}`, complete: false };
}

async function copyAgentMaterialCommand(kind, copiedMessage, toastMessage) {
  const jobId = state.materialEditorJobId;
  const button = kind === "resume" ? elements.copyResumeAiCommand : elements.copyCoverLetterAiCommand;
  if (!jobId || button.disabled || api.capabilities.material_integrity === 1) return;
  const { command, complete } = await buildAgentMaterialCommand(jobId, kind);
  const hint = complete
    ? copiedMessage
    : `${copiedMessage} Set $agent and $connection from Settings > Local agent connection in the desktop app.`;
  try {
    await navigator.clipboard.writeText(command);
    elements.materialEditorMessage.className = "form-message";
    elements.materialEditorMessage.textContent = hint;
    toast.show({ message: toastMessage });
  } catch {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = command;
  }
}

function copyCoverLetterAiCommand() {
  return copyAgentMaterialCommand(
    "cover_letter",
    "Bounded agent command copied. It returns the current draft and evidence catalog without broad Vault rereads.",
    "Build-with-AI command copied.",
  );
}

function syncResumeMaterialSimpleField(target) {
  const draft = state.resumeMaterialEditor?.draft;
  if (!draft) return false;
  const bindings = new Map([
    [elements.resumeMaterialName, [draft.candidate, "name"]],
    [elements.resumeMaterialEmail, [draft.candidate, "email"]],
    [elements.resumeMaterialLocation, [draft.candidate, "location_line"]],
    [elements.resumeMaterialPhone, [draft.candidate, "phone"]],
    [elements.resumeMaterialLinkedin, [draft.candidate, "linkedin"]],
    [elements.resumeMaterialGithub, [draft.candidate, "github"]],
    [elements.resumeMaterialWebsite, [draft.candidate, "website"]],
    [elements.resumeMaterialSummary, [draft, "summary"]],
  ]);
  const binding = bindings.get(target);
  if (!binding) return false;
  binding[0][binding[1]] = target.value;
  return true;
}

function handleResumeMaterialInput(event) {
  const editor = state.resumeMaterialEditor;
  if (!editor || state.resumeMaterialLoading) return;
  const target = event.target;
  let changed = syncResumeMaterialSimpleField(target);
  const collection = target.dataset.resumeBulletCollection;
  if (collection) {
    const record = editor.draft[collection]?.[Number(target.dataset.resumeRecordIndex)];
    const bullet = record?.bullets?.[Number(target.dataset.resumeBulletIndex)];
    if (bullet) {
      bullet.text = target.value;
      bullet.origin = "user";
      changed = true;
    }
  }
  if (!changed) return;
  state.resumeMaterialDirty = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Resume changes are application-only until you save.";
  syncMaterialReviews();
  elements.materialEditorStatus.textContent = "Unsaved changes";
  elements.resumeMaterialIssueCount.textContent = "Save to validate";
  elements.saveResumeMaterialButton.disabled = false;
  elements.approveResumeMaterialButton.disabled = true;
  elements.resumeMaterialExportButtons.forEach((button) => { button.disabled = true; });
  elements.resumeMaterialExportNote.textContent = "Save a valid draft before exporting.";
}

async function saveResumeMaterial(approve) {
  const editor = state.resumeMaterialEditor;
  const jobId = state.materialEditorJobId;
  if (!editor || !jobId || state.resumeMaterialLoading) return;
  if (!elements.resumeMaterialForm.reportValidity()) return;
  state.resumeMaterialLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = approve
    ? "Checking and approving application resume..."
    : "Validating and saving application resume...";
  renderApplicationMaterials();
  try {
    const saved = await api.saveResumeMaterial(jobId, {
      action: approve ? "approve" : "save_draft",
      expectedRevision: editor.revision,
      draft: editor.draft,
    });
    state.resumeMaterialEditor = saved;
    state.resumeMaterialDirty = false;
    elements.materialEditorMessage.textContent = approve
      ? "Application resume approved. Final checks run before export."
      : "Application resume draft saved and validated.";
    toast.show({ message: approve ? "Application resume approved." : "Resume draft saved." });
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeMaterialLoading = false;
    renderApplicationMaterials();
  }
}

function rebuildResumeMaterialFromPlan() {
  const editor = state.resumeMaterialEditor;
  if (!editor?.source_defaults) return;
  if (!window.confirm("Update this resume from the current content selection? Unsaved custom wording will be replaced.")) return;
  editor.draft = structuredClone(editor.source_defaults);
  state.resumeMaterialDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Rebuilt from current sources. Review the summary and bullets, then save.";
}

async function downloadResumeMaterial(format) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.resumeMaterialLoading) return;
  if (!await materialReviews.resume.ensureReady() || state.materialEditorJobId !== jobId || state.resumeMaterialDirty) return;
  state.resumeMaterialLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = `Rendering and validating resume ${format.toUpperCase()}...`;
  renderApplicationMaterials();
  try {
    const { blob, filename } = await api.downloadResumeMaterial(jobId, format);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.materialEditorMessage.textContent = `${filename} passed structural checks and was downloaded.`;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeMaterialLoading = false;
    renderApplicationMaterials();
  }
}

function copyResumeAiCommand() {
  return copyAgentMaterialCommand(
    "resume",
    "Bounded resume command copied. It exposes only selected Career Vault evidence and editable prose.",
    "Resume Build-with-AI command copied.",
  );
}

async function returnFromMaterialEditor({ selectResume = false } = {}) {
  if (!(await confirmReviewNavigation(state.materialEditorReturnView))) return;
  const jobId = state.materialEditorJobId;
  switchView(state.materialEditorReturnView);
  if (state.materialEditorReturnView === "job-library" && jobId) await selectJob(jobId);
  if (selectResume) elements.applicationResumeCombobox?.scrollIntoView({ behavior: "smooth", block: "center" });
}

async function saveInlineApplicationStatus(select) {
  const job = state.jobs.find((item) => item.id === select.dataset.applicationJobId);
  if (!job?.valid || state.jobApplicationLoading) return;
  const previousStatus = select.dataset.previousStatus;
  const status = select.value;
  if (status === previousStatus) return;
  select.disabled = true;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = `Saving ${job.company || "application"}: ${status}...`;
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/status`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status, occurred_at: todayInputValue(), note: "" }),
      }
    );
    storeJobApplication(application);
    elements.applicationLedgerMessage.textContent = `Saved ${job.company || job.title}: ${status}.`;
  } catch (error) {
    select.value = previousStatus;
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    renderJobs();
  }
}

async function saveApplicationStatus(event) {
  event.preventDefault();
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobApplicationLoading) return;
  const status = elements.applicationStatusSelect.value;
  const note = elements.applicationStatusNote.value.trim();
  if (status === state.jobApplication?.current_status && !note) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = "Choose a different status or add a note.";
    return;
  }
  state.jobApplicationLoading = true;
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Adding status update...";
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/status`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          status,
          occurred_at: elements.applicationStatusDate.value || null,
          note,
        }),
      }
    );
    storeJobApplication(application);
    elements.applicationStatusNote.value = "";
    elements.applicationMessage.textContent = `Status added: ${status}.`;
    toast.show({ message: `Application status updated to ${applicationStatusLabel(status)}.` });
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.jobApplicationLoading = false;
    renderJobs();
  }
}
function renderJobAssessment() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobAssessmentLoading) {
    elements.jobAssessment.classList.add("hidden");
    return;
  }
  if (!state.jobAssessment && !state.jobAssessmentError) {
    elements.jobAssessment.classList.add("hidden");
    return;
  }
  elements.jobAssessment.classList.remove("hidden");
  elements.jobAssessment.innerHTML = renderAssessmentSummary(
    state.jobAssessmentError ? null : state.jobAssessment,
    { job, profile: state.profileRecord, title: "Application assessment" },
  );
}

function openAssessmentResolution(button) {
  const findingKey = button?.dataset.assessmentFindingKey || "";
  if (!findingKey || !state.jobAssessment) return;
  const existing = (state.jobAssessment.user_resolutions || []).find(
    (item) => item.finding_key === findingKey,
  );
  state.assessmentResolutionFindingKey = findingKey;
  state.assessmentResolutionEditTarget = button.dataset.assessmentEditTarget || "";
  elements.assessmentResolutionFinding.textContent = button.dataset.assessmentFindingLabel
    || findingKey;
  const normalizedExisting = ["accepted_risk"].includes(existing?.resolution)
    ? "accepted_risk"
    : existing
      ? "resolved"
      : ["warning", "danger"].includes(button.dataset.assessmentFindingTone)
        ? "accepted_risk"
        : "resolved";
  elements.assessmentResolutionValues.forEach((input) => {
    input.checked = input.value === normalizedExisting;
  });
  elements.assessmentResolutionNote.value = existing?.note || "";
  elements.assessmentResolutionMessage.className = "form-message";
  elements.assessmentResolutionMessage.textContent = existing?.stale
    ? "The assessment changed after this decision. Record a new decision to confirm it again."
    : "";
  elements.assessmentResolutionDialog.showModal();
  elements.assessmentResolutionValues.find((input) => input.checked)?.focus();
}

function closeAssessmentResolution() {
  state.assessmentResolutionFindingKey = "";
  state.assessmentResolutionEditTarget = "";
  if (elements.assessmentResolutionDialog.open) {
    elements.assessmentResolutionDialog.close();
  }
}

async function saveAssessmentResolution(event) {
  event.preventDefault();
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const findingKey = state.assessmentResolutionFindingKey;
  if (!job?.valid || !findingKey) return;
  const revisioned = api.capabilities.assessment_resolution_revision === true;
  const assessmentRevision = state.jobAssessment?.assessment_revision || "";
  if (revisioned && !/^sha256:[0-9a-f]{64}$/.test(assessmentRevision)) {
    elements.assessmentResolutionMessage.className = "form-message error";
    elements.assessmentResolutionMessage.textContent = "Assessment revision unavailable. Close this review and reload the assessment.";
    return;
  }
  elements.saveAssessmentResolutionButton.disabled = true;
  elements.assessmentResolutionMessage.className = "form-message";
  elements.assessmentResolutionMessage.textContent = "Recording decision...";
  try {
    const payload = {
      finding_key: findingKey,
      resolution: elements.assessmentResolutionValues.find((input) => input.checked)?.value || "accepted_risk",
      note: elements.assessmentResolutionNote.value,
    };
    if (revisioned) payload.expected_assessment_revision = assessmentRevision;
    const assessment = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/assessment/resolution`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      },
    );
    state.jobAssessment = assessment;
    if (job.application) job.application.assessment = assessment;
    closeAssessmentResolution();
    renderJobAssessment();
    setJobWorkspaceTab(state.jobWorkspaceTab);
    renderJobApplication();
    toast.show({ message: "Assessment decision recorded." });
  } catch (error) {
    if (revisioned && error?.payload?.error_code === "JOB_REVISION_CONFLICT") {
      closeAssessmentResolution();
      await loadSelectedJobAssessment();
      toast.show({ message: "Assessment changed. Review the latest findings before recording a decision." });
      return;
    }
    elements.assessmentResolutionMessage.className = "form-message error";
    elements.assessmentResolutionMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveAssessmentResolutionButton.disabled = false;
  }
}

function correctAssessmentFacts() {
  const target = state.assessmentResolutionEditTarget;
  closeAssessmentResolution();
  if (target === "eligibility") {
    setJobWorkspaceTab("assessment");
    openJobGatesEditor();
    elements.jobEligibilitySection.scrollIntoView({ behavior: "smooth", block: "center" });
    return;
  }
  if (target === "requirements") {
    setJobWorkspaceTab("assessment");
    openJobRequirementsEditor();
    elements.jobRequirementsSection.scrollIntoView({ behavior: "smooth", block: "center" });
    return;
  }
  setJobWorkspaceTab("details");
  openJobEditForm();
  elements.jobFactGrid.scrollIntoView({ behavior: "smooth", block: "center" });
}

async function loadSelectedJobAssessment() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) {
    state.jobAssessment = null;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
    renderJobAssessment();
    return;
  }
  const requestedJobId = job.id;
  state.jobAssessment = null;
  state.jobAssessmentLoading = true;
  state.jobAssessmentError = "";
  renderJobAssessment();
  try {
    const assessment = await requestJson(`/api/jobs/${encodeURIComponent(requestedJobId)}/assessment`);
    if (state.selectedJobId !== requestedJobId) return;
    state.jobAssessment = assessment;
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobAssessmentError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobAssessmentLoading = false;
      renderJobAssessment();
    }
  }
}

function hideJobEditForm({ render = true } = {}) {
  state.jobIdentityEditing = false;
  elements.editJobDetailsButton.setAttribute("aria-expanded", "false");
  elements.jobEditMessage.className = "form-message";
  elements.jobEditMessage.textContent = "";
  if (render) renderJobDigest();
}

function openJobEditForm() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobEditSaving) return;
  state.jobIdentityEditing = true;
  setJobWorkspaceTab("details");
  hideJobEditMessage();
  elements.editJobDetailsButton.setAttribute("aria-expanded", "true");
  renderJobDigest();
  document.querySelector("#job-edit-company")?.focus();
}

function hideJobEditMessage() {
  elements.jobEditMessage.className = "form-message";
  elements.jobEditMessage.textContent = "";
}

async function persistJobFacts(job, fields) {
  let result;
  try {
    result = await api.saveJobDetails(job.id, fields, job.card_revision);
  } catch (error) {
    const operation = error?.payload?.operation_state;
    if (api.capabilities.job_details_revision === true && (
      error?.payload?.error_code === "JOB_DETAILS_REVISION_CONFLICT" ||
      operation?.committed === true || operation?.known === false
    )) {
      // Read the exact job, including when it was selected from a later page.
      // Never re-submit an edit based on a card the user has not reviewed.
      try {
        const latest = await api.getJob(job.id);
        const index = state.jobs.findIndex((item) => item.id === job.id);
        if (index >= 0 && latest?.job?.valid) state.jobs[index] = { ...state.jobs[index], ...latest.job };
        renderJobs();
        if (state.selectedJobId === job.id) await loadSelectedJobAssessment();
      } catch { /* Original conflict is still the actionable error. */ }
    }
    throw error;
  }
  const index = state.jobs.findIndex((item) => item.id === result.job.id);
  if (index >= 0) state.jobs[index] = { ...state.jobs[index], ...result.job };
  renderJobs();
  if (state.selectedJobId === job.id) await loadSelectedJobAssessment();
  return result.job;
}

async function updateSelectedJobFacts(fields) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) throw new Error("The selected job is no longer available.");
  return persistJobFacts(job, fields);
}

async function saveJobDetails() {
  if (state.jobEditSaving) return;
  state.jobEditSaving = true;
  elements.saveJobEditButton.disabled = true;
  elements.saveJobEditButton.textContent = "Saving...";
  hideJobEditMessage();
  try {
    await updateSelectedJobFacts({
      company: document.querySelector("#job-edit-company")?.value || "",
      title: document.querySelector("#job-edit-title")?.value || "",
      seniority: document.querySelector("#job-edit-seniority")?.value || "",
      location: document.querySelector("#job-edit-location")?.value || "",
      remote_policy: document.querySelector("#job-edit-remote-policy")?.value || "",
      source_url: document.querySelector("#job-edit-source-url")?.value || "",
    });
    state.jobIdentityEditing = false;
    renderJobDigest();
    toast.show({ message: "Job facts updated." });
  } catch (error) {
    elements.jobEditMessage.className = "form-message error";
    elements.jobEditMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobEditButton.disabled = false;
    elements.saveJobEditButton.textContent = "Save";
  }
}

function setJobGateEditing(editing) {
  state.jobGatesEditing = editing;
  elements.jobGatesMessage.className = "form-message";
  elements.jobGatesMessage.textContent = "";
  renderJobDigest();
}

function openJobGatesEditor() {
  setJobWorkspaceTab("assessment");
  setJobGateEditing(true);
  document.querySelector("[data-job-gate-name]")?.focus();
}

async function saveJobGates() {
  if (state.jobEditSaving) return;
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) return;
  const currentGates = job.digest?.eligibility_gates || {};
  const eligibilityGates = {};
  document.querySelectorAll("[data-job-gate-name]").forEach((select) => {
    const name = select.dataset.jobGateName;
    const original = currentGates[name] || {};
    const changed = select.value !== original.status;
    eligibilityGates[name] = {
      status: select.value,
      reasons: changed ? ["Manually reviewed for this saved job."] : (original.reasons || []),
      evidence: changed ? [] : (original.evidence || []),
    };
  });
  const byName = Object.fromEntries(
    [...document.querySelectorAll("[data-job-gate-name]")].map((select) => [select.dataset.jobGateName, select.value]),
  );
  const visaSignals = { ...job.visa_signals };
  visaSignals.citizenship_required = byName.citizenship === "not_stated" ? null : true;
  visaSignals.clearance_required = byName.clearance === "not_stated" ? null : true;
  if (byName.sponsorship === "incompatible") {
    visaSignals.no_visa_or_opt = true;
    visaSignals.sponsorship_mentioned = true;
  } else if (byName.sponsorship === "not_stated") {
    visaSignals.no_visa_or_opt = null;
    visaSignals.sponsorship_mentioned = null;
  } else {
    visaSignals.no_visa_or_opt = null;
    visaSignals.sponsorship_mentioned = true;
  }
  state.jobEditSaving = true;
  elements.saveJobGatesButton.disabled = true;
  try {
    await updateSelectedJobFacts({ eligibility_gates: eligibilityGates, visa_signals: visaSignals });
    state.jobGatesEditing = false;
    renderJobDigest();
    toast.show({ message: "Eligibility facts updated." });
  } catch (error) {
    elements.jobGatesMessage.className = "form-message error";
    elements.jobGatesMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobGatesButton.disabled = false;
  }
}

function setJobRequirementsEditing(editing) {
  state.jobRequirementsEditing = editing;
  elements.jobRequirementsMessage.className = "form-message";
  elements.jobRequirementsMessage.textContent = "";
  renderJobDigest();
}

function openJobRequirementsEditor() {
  setJobWorkspaceTab("details");
  setJobRequirementsEditing(true);
  document.querySelector("#job-edit-required-skills")?.focus();
}

function linesFromJobEditor(id) {
  return vaultLines(document.querySelector(id)?.value || "", true);
}

async function saveJobRequirements() {
  if (state.jobEditSaving) return;
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) return;
  const experienceRequirements = [...document.querySelectorAll("[data-job-experience-row]")]
    .map((row) => {
      const minimum = Number(row.querySelector("[data-experience-min]")?.value);
      const maximumValue = row.querySelector("[data-experience-max]")?.value;
      const area = row.querySelector("[data-experience-area]")?.value.trim() || "";
      if (!area || !Number.isFinite(minimum) || minimum < 0) return null;
      const item = { minimum_years: minimum, area };
      if (maximumValue !== "") item.maximum_years = Number(maximumValue);
      return item;
    })
    .filter(Boolean);
  const classified = (job.digest?.classified_requirements || []).filter((_, index) => (
    document.querySelector(`[data-classified-requirement-index="${index}"]`)?.checked
  ));
  state.jobEditSaving = true;
  elements.saveJobRequirementsButton.disabled = true;
  try {
    await updateSelectedJobFacts({
      required_skills: linesFromJobEditor("#job-edit-required-skills"),
      required_platforms: linesFromJobEditor("#job-edit-required-platforms"),
      required_capabilities: linesFromJobEditor("#job-edit-required-capabilities"),
      preferred_skills: linesFromJobEditor("#job-edit-preferred-skills"),
      education_requirements: linesFromJobEditor("#job-edit-education-requirements"),
      experience_requirements: experienceRequirements,
      classified_requirements: classified,
    });
    state.jobRequirementsEditing = false;
    renderJobDigest();
    toast.show({ message: "Requirement facts updated." });
  } catch (error) {
    elements.jobRequirementsMessage.className = "form-message error";
    elements.jobRequirementsMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobRequirementsButton.disabled = false;
  }
}

const JOB_INSTRUCTION_CATEGORY_LABELS = {
  email_destination: "Email destination",
  exact_phrase: "Exact phrase or subject",
  required_artifact: "Required artifact",
  cover_letter_format: "Cover-letter or question format",
  external_step: "External step",
  deadline: "Deadline",
};

function renderJobInstructions() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const available = Boolean(job?.valid);
  elements.jobInstructionChecklist.classList.toggle("hidden", !available);
  if (!available) return;
  elements.addSelectedJobInstruction.disabled = state.jobInstructionSaving;
  elements.jobInstructionCategory.disabled = state.jobInstructionSaving;
  if (state.jobInstructionsLoading) {
    elements.jobInstructionCount.textContent = "Loading";
    elements.jobInstructionList.innerHTML = renderLoadingState({
      label: "Checking exact posting instructions…",
      compact: true,
    });
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  if (state.jobInstructionsError) {
    elements.jobInstructionCount.textContent = "Unavailable";
    elements.jobInstructionList.innerHTML = `<div class="empty-state error">${escapeHtml(state.jobInstructionsError)}</div>`;
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  const checklist = state.jobInstructions;
  if (!checklist || checklist.job_id !== job.id) {
    elements.jobInstructionCount.textContent = "Not loaded";
    elements.jobInstructionList.innerHTML = '<div class="empty-state">Checklist is not loaded.</div>';
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  const items = Array.isArray(checklist.items) ? checklist.items : [];
  const counts = checklist.counts || {};
  elements.jobInstructionCount.textContent = items.length
    ? `${Number(counts.done || 0)} done · ${items.length} total`
    : "0 flagged";
  elements.jobInstructionList.innerHTML = items.length
    ? items.map((item) => {
      const lineLabel = item.line_start === item.line_end
        ? `Line ${item.line_start}`
        : `Lines ${item.line_start}–${item.line_end}`;
      const staleCopy = item.stale
        ? `<p class="job-instruction-stale-note">The posting changed since this was marked ${escapeHtml((item.previous_state || "pending").replaceAll("_", " "))}. Confirm it again against the current source.</p>`
        : "";
      return `<article class="job-instruction-item${item.stale ? " is-stale" : ""}" data-instruction-state="${escapeHtml(item.state)}">
        <header>
          <div><strong>${escapeHtml(JOB_INSTRUCTION_CATEGORY_LABELS[item.category] || item.category)}</strong><span>${escapeHtml(item.origin === "manual" ? "Added by you" : item.confidence === "explicit" ? "Explicit" : "Review")}</span></div>
          <small>${escapeHtml(lineLabel)} · v${escapeHtml(item.extractor_version)}</small>
        </header>
        <blockquote translate="no">${escapeHtml(item.source_text)}</blockquote>
        ${staleCopy}
        <div class="job-instruction-actions" role="group" aria-label="Checklist state">
          ${[
            ["pending", "Pending"],
            ["done", "Done"],
            ["not_applicable", "Not applicable"],
          ].map(([value, label]) => `<button type="button" class="text-button${item.state === value ? " is-active" : ""}" data-job-instruction-action="${value}" data-job-instruction-fingerprint="${escapeHtml(item.fingerprint)}" aria-pressed="${item.state === value}"${state.jobInstructionSaving ? " disabled" : ""}>${label}</button>`).join("")}
        </div>
      </article>`;
    }).join("")
    : `<div class="job-instruction-empty"><strong>No explicit extra step detected.</strong><span>This is not a guarantee. You can add exact selected text from the posting below.</span></div>`;
  const staleItems = Array.isArray(checklist.stale_items) ? checklist.stale_items : [];
  elements.jobInstructionStale.innerHTML = staleItems.length
    ? `<details><summary>${staleItems.length} stale ${staleItems.length === 1 ? "instruction" : "instructions"} from an earlier posting version</summary><div>${staleItems.map((item) => `<article><strong>${escapeHtml(JOB_INSTRUCTION_CATEGORY_LABELS[item.category] || item.category)}</strong><blockquote translate="no">${escapeHtml(item.source_text)}</blockquote><small>${escapeHtml((item.state || "pending").replaceAll("_", " "))}</small></article>`).join("")}</div></details>`
    : "";
}

const JOB_CONTACT_CONTEXT_LABELS = {
  application_channel: "Application channel",
  application_recipient: "Application recipient",
  recruiter: "Recruiter",
  hiring_manager: "Hiring manager",
  possible_team_contact: "Possible team contact",
};

const JOB_CONTACT_CHANNEL_LABELS = {
  email: "Email",
  phone: "Phone",
  professional_profile: "Professional profile",
};

function renderJobContactCandidates() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const available = Boolean(job?.valid);
  elements.jobContactCandidates.classList.toggle("hidden", !available);
  if (!available) return;
  const analysis = job?.digest?.contact_candidates;
  const candidates = Array.isArray(analysis?.candidates) ? analysis.candidates : [];
  if (job.analysis_stale || !analysis) {
    elements.jobContactCandidateCount.textContent = "Analysis update available";
    elements.jobContactCandidateList.innerHTML = '<div class="job-instruction-empty"><strong>Refresh the analysis to check the current posting.</strong><span>No contact candidate is inferred from stale or missing analysis.</span></div>';
    return;
  }
  elements.jobContactCandidateCount.textContent = candidates.length
    ? `${candidates.length} flagged`
    : "0 flagged";
  elements.jobContactCandidateList.innerHTML = candidates.length
    ? candidates.map((candidate) => {
      const lineLabel = candidate.line_start === candidate.line_end
        ? `Line ${candidate.line_start}`
        : `Lines ${candidate.line_start}–${candidate.line_end}`;
      const channels = (Array.isArray(candidate.channels) ? candidate.channels : [])
        .map((channel) => `<li><span>${escapeHtml(JOB_CONTACT_CHANNEL_LABELS[channel.kind] || channel.kind)}</span><strong translate="no">${escapeHtml(channel.value || "")}</strong></li>`)
        .join("");
      const facts = [candidate.role, candidate.company].filter(Boolean);
      const title = candidate.name || candidate.channels?.[0]?.value || "Application channel";
      return `<article class="contact-candidate-card">
        <header><div><strong>${escapeHtml(title)}</strong>${facts.length ? `<span>${escapeHtml(facts.join(" · "))}</span>` : ""}</div><span>${escapeHtml(candidate.confidence === "explicit" ? "Explicit" : "Review")}</span></header>
        <p class="contact-candidate-context">${escapeHtml(JOB_CONTACT_CONTEXT_LABELS[candidate.context_kind] || candidate.context_kind)}</p>
        ${channels ? `<ul class="contact-candidate-channels">${channels}</ul>` : '<p class="contact-candidate-no-channel">No direct channel stated</p>'}
        <blockquote translate="no">${escapeHtml(candidate.source_text || "")}</blockquote>
        <footer><small>${escapeHtml(lineLabel)} · extractor v${escapeHtml(candidate.extractor_version)}</small><button class="secondary-button compact-button" type="button" data-save-contact-candidate data-contact-source-kind="job" data-contact-source-id="${escapeHtml(job.id)}" data-contact-candidate-id="${escapeHtml(candidate.candidate_id)}" data-contact-content-sha256="${escapeHtml(candidate.content_sha256)}">Save contact</button></footer>
      </article>`;
    }).join("")
    : '<div class="job-instruction-empty"><strong>No explicit contact candidate detected.</strong><span>This narrow deterministic check does not prove that the posting contains no useful person or channel.</span></div>';
}

async function loadSelectedJobInstructions() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) {
    state.jobInstructions = null;
    state.jobInstructionsLoading = false;
    state.jobInstructionsError = "";
    renderJobInstructions();
    return;
  }
  const requestedJobId = job.id;
  state.jobInstructions = null;
  state.jobInstructionsLoading = true;
  state.jobInstructionsError = "";
  renderJobInstructions();
  try {
    const checklist = await api.getApplicationInstructions(requestedJobId);
    if (state.selectedJobId !== requestedJobId) return;
    state.jobInstructions = checklist;
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobInstructionsError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobInstructionsLoading = false;
      renderJobInstructions();
    }
  }
}

async function saveJobInstructionState(fingerprint, nextState) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const checklist = state.jobInstructions;
  if (!job?.valid || !checklist || state.jobInstructionSaving) return;
  state.jobInstructionSaving = true;
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "Saving checklist state…";
  renderJobInstructions();
  try {
    state.jobInstructions = await api.setApplicationInstructionState(
      job.id,
      checklist.source_fingerprint,
      fingerprint,
      nextState,
    );
    elements.jobInstructionMessage.className = "form-message success";
    elements.jobInstructionMessage.textContent = "Checklist updated.";
  } catch (error) {
    if (error?.payload?.error_code === "APPLICATION_INSTRUCTION_REVISION_CONFLICT") {
      await loadSelectedJobInstructions();
    }
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = formatRequestError(error);
  } finally {
    state.jobInstructionSaving = false;
    renderJobInstructions();
  }
}

async function addSelectedJobInstruction() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const checklist = state.jobInstructions;
  if (!job?.valid || !checklist || state.jobInstructionSaving) return;
  const sourcePreview = state.jobSourcePreview?.jobId === job.id
    ? state.jobSourcePreview
    : null;
  if (!sourcePreview) {
    elements.jobInstructionMessage.className = "form-message";
    elements.jobInstructionMessage.textContent = "Loading the original posting. Select exact text there, then click Add selected text again.";
    await loadSelectedJobSource();
    return;
  }
  const selection = window.getSelection();
  const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
  if (!range || !elements.jobSourcePreviewText.contains(range.commonAncestorContainer)) {
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = "Select exact text inside Original job description first.";
    return;
  }
  const selectedText = selection.toString().trim();
  if (!selectedText) {
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = "Select exact text from the posting first.";
    return;
  }
  state.jobInstructionSaving = true;
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "Adding exact source text…";
  renderJobInstructions();
  try {
    state.jobInstructions = await api.addManualApplicationInstruction(
      job.id,
      checklist.source_fingerprint,
      elements.jobInstructionCategory.value,
      selectedText,
    );
    selection.removeAllRanges();
    elements.jobInstructionMessage.className = "form-message success";
    elements.jobInstructionMessage.textContent = "Exact posting instruction added.";
  } catch (error) {
    if (error?.payload?.error_code === "APPLICATION_INSTRUCTION_REVISION_CONFLICT") {
      await loadSelectedJobInstructions();
    }
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = formatRequestError(error);
  } finally {
    state.jobInstructionSaving = false;
    renderJobInstructions();
  }
}

function renderJobDigest() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job) {
    elements.jobDigestSection.classList.add("hidden");
    elements.jobAssessment.classList.add("hidden");
    elements.jobDigestSourceLink.classList.add("hidden");
    elements.jobSourcePreview.classList.add("hidden");
    hideJobEditForm({ render: false });
    state.jobGatesEditing = false;
    state.jobRequirementsEditing = false;
    return;
  }
  const digest = job.digest || {};
  const fields = digest.fields || {};
  const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
  elements.jobDigestSection.classList.remove("hidden");
  elements.editJobDetailsButton.disabled = !job.valid || state.jobEditSaving;
  elements.editJobDetailsButton.classList.toggle("hidden", state.jobIdentityEditing);
  elements.cancelJobEditButton.classList.toggle("hidden", !state.jobIdentityEditing);
  elements.saveJobEditButton.classList.toggle("hidden", !state.jobIdentityEditing);
  elements.jobDigestTitle.textContent = role;
  elements.jobDigestStatus.textContent = job.valid ? job.status : "invalid";
  elements.jobDigestStatus.className = `status-text ${statusClass(job.valid ? job.status : "error")}`;
  if (job.source_url && !state.jobIdentityEditing) {
    elements.jobDigestSourceLink.href = job.source_url;
    elements.jobDigestSourceLink.classList.remove("hidden");
  } else {
    elements.jobDigestSourceLink.href = "#";
    elements.jobDigestSourceLink.classList.add("hidden");
  }
  const sourcePreview = state.jobSourcePreview?.jobId === job.id
    ? state.jobSourcePreview
    : null;
  elements.jobSourcePreview.classList.toggle("hidden", !sourcePreview);
  elements.jobSourcePreviewMeta.textContent = sourcePreview
    ? `${sourcePreview.sourceName} | ${sourcePreview.sourceText.length.toLocaleString()} characters`
    : "";
  elements.jobSourcePreviewText.textContent = sourcePreview?.sourceText || "";

  const positionFacts = [
    ["Company", "company"],
    ["Role", "title"],
    ["Seniority", "seniority"],
  ];
  const contextFacts = [
    ["Location", "location"],
    ["Workplace", "remote_policy"],
    ["Source URL", "source_url"],
  ];
  elements.jobFactGrid.innerHTML = renderJobIdentityFields({
    fields,
    facts: positionFacts,
    values: job,
    editing: state.jobIdentityEditing,
    manualOverrides: job.manual_overrides || [],
  });
  elements.jobContextGrid.innerHTML = renderJobIdentityFields({
    fields,
    facts: contextFacts,
    values: job,
    editing: state.jobIdentityEditing,
    manualOverrides: job.manual_overrides || [],
  });

  const compensationRanges = job.compensation?.ranges || digest.compensation?.ranges || [];
  elements.jobCompensationList.innerHTML = compensationRanges.length
    ? compensationRanges.map((range) => {
      const evidence = range.evidence?.[0];
      const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Source-backed compensation";
      const context = [
        range.location && range.location !== fields.location?.value ? range.location : "",
        range.period && range.period !== "unspecified" ? `per ${range.period}` : "",
      ].filter(Boolean).join(" | ");
      return `<div class="job-compensation-range" title="${escapeHtml(evidenceTitle)}">
        <span>Compensation</span>
        <strong>${escapeHtml(formatJobCompensationRange(range))}</strong>
        ${context ? `<small>${escapeHtml(context)}</small>` : ""}
      </div>`;
    }).join("")
    : `<div class="job-compensation-empty">
        <span>Compensation</span>
        <strong>Not stated in this posting</strong>
      </div>`;

  const gates = digest.eligibility_gates || {};
  elements.editJobGatesButton.classList.toggle("hidden", state.jobGatesEditing);
  elements.cancelJobGatesButton.classList.toggle("hidden", !state.jobGatesEditing);
  elements.saveJobGatesButton.classList.toggle("hidden", !state.jobGatesEditing);
  elements.jobGateList.innerHTML = Object.entries(gates).map(([name, gate]) => {
    const reason = gate.reasons?.join(" ") || "No explicit requirement found.";
    const evidence = gate.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : reason;
    const statusControl = state.jobGatesEditing
      ? `<select data-job-gate-name="${escapeHtml(name)}" aria-label="${escapeHtml(name.replaceAll("_", " "))} status">${["not_stated", "compatible", "unclear", "incompatible"].map((status) => `<option value="${status}"${gate.status === status ? " selected" : ""}>${escapeHtml(status.replaceAll("_", " "))}</option>`).join("")}</select>`
      : `<strong class="job-gate-status ${escapeHtml(gate.status)}">${escapeHtml(gate.status.replaceAll("_", " "))}</strong>`;
    return `<div class="job-gate" title="${escapeHtml(evidenceTitle)}">
      <div class="job-gate-head">
        <span>${escapeHtml(name)}</span>
        ${statusControl}
      </div>
      <div>${escapeHtml(reason)}</div>
    </div>`;
  }).join("");

  const signalGroups = [
    ["Required", job.required_skills || [], job.required_platforms || []],
    ["Preferred", job.preferred_skills || []],
    ["Mentioned", digest.mentioned_technologies || []],
  ];
  const occurrenceByName = new Map();
  (digest.skill_occurrences || []).forEach((occurrence) => {
    if (!occurrenceByName.has(occurrence.canonical_name)) {
      occurrenceByName.set(occurrence.canonical_name, occurrence);
    }
  });
  const manuallyReviewedSignals = new Set();
  if ((job.manual_overrides || []).includes("required_skills")) (job.required_skills || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("required_platforms")) (job.required_platforms || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("preferred_skills")) (job.preferred_skills || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("mentioned_technologies")) (digest.mentioned_technologies || []).forEach((name) => manuallyReviewedSignals.add(name));
  const renderSignalSkill = (technology) => {
    const occurrence = occurrenceByName.get(technology);
    const origin = manuallyReviewedSignals.has(technology) ? "manual" : occurrence?.origin;
    const originClass = origin ? ` registry-skill-${escapeHtml(origin)}` : "";
    const detail = occurrence
      ? `Line ${occurrence.line}: ${occurrence.source_text} | ${occurrence.primary_group_id} | ${origin}`
      : origin === "manual" ? "Manually attached to this job" : "Detected registry skill";
    return `<span class="tag${originClass}" title="${escapeHtml(detail)}">${escapeHtml(technology)}</span>`;
  };
  const technologyHtml = signalGroups.map(([level, ...groups]) => {
    const technologies = [...new Set(groups.flat())];
    if (!technologies.length) return "";
    return `<div class="job-technology-group">
      <span>${escapeHtml(level)}</span>
      <div>${technologies.map(renderSignalSkill).join("")}</div>
    </div>`;
  }).filter(Boolean);
  const alternativeHtml = (job.technology_groups || digest.technology_groups || []).map((group) => {
    const technologies = [...new Set(group.technologies || [])];
    if (!technologies.length) return "";
    const evidence = group.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Alternative requirement";
    const requirementLabel = group.requirement === "required"
      ? "Required: choose at least one"
      : group.requirement === "preferred"
        ? "Preferred alternatives"
        : "Mentioned alternatives";
    return `<div class="job-technology-group job-technology-alternative" title="${escapeHtml(evidenceTitle)}">
      <span>${escapeHtml(requirementLabel)}</span>
      <div>${technologies.map(renderSignalSkill).join("")}</div>
    </div>`;
  }).filter(Boolean);
  elements.jobTechnologyList.innerHTML = technologyHtml.length
    || alternativeHtml.length
    ? [...technologyHtml, ...alternativeHtml].join("")
    : '<span class="empty-state">No technology signals detected.</span>';

  const experienceHtml = (job.experience_requirements || []).map((requirement) => {
    const years = requirement.maximum_years === undefined
      ? `${requirement.minimum_years}+ years`
      : `${requirement.minimum_years}-${requirement.maximum_years} years`;
    return `<li><strong>${escapeHtml(years)}</strong><span translate="no">${escapeHtml(requirement.area)}</span></li>`;
  });
  const educationHtml = (job.education_requirements || []).map((requirement) => (
    `<li><strong>Education</strong><span translate="no">${escapeHtml(requirement)}</span></li>`
  ));
  const capabilityHtml = (job.required_capabilities || []).map((requirement) => (
    `<li><strong>Required capability</strong><span translate="no">${escapeHtml(requirement)}</span></li>`
  ));
  const classifiedHtml = (digest.classified_requirements || []).map((requirement) => {
    const level = requirement.requirement === "preferred" ? "Preferred" : "Required";
    const category = requirement.category || "requirement";
    const evidence = requirement.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Source evidence unavailable";
    return `<li class="job-classified-requirement" title="${escapeHtml(evidenceTitle)}">
      <strong>${escapeHtml(`${level} ${category}`)}</strong>
      <span translate="no"><b>${escapeHtml(requirement.name)}</b><small>${escapeHtml(requirement.text)}</small></span>
    </li>`;
  });
  const requirementHtml = [...experienceHtml, ...educationHtml, ...capabilityHtml, ...classifiedHtml];
  elements.editJobRequirementsButton.classList.toggle("hidden", state.jobRequirementsEditing);
  elements.cancelJobRequirementsButton.classList.toggle("hidden", !state.jobRequirementsEditing);
  elements.saveJobRequirementsButton.classList.toggle("hidden", !state.jobRequirementsEditing);
  if (state.jobRequirementsEditing) {
    const experienceRows = [...(job.experience_requirements || []), { minimum_years: "", maximum_years: "", area: "" }]
      .map((requirement) => `<div class="job-experience-edit-row" data-job-experience-row>
        <label><span>Min years</span><input type="number" min="0" step="0.5" data-experience-min value="${escapeHtml(requirement.minimum_years ?? "")}"></label>
        <label><span>Max</span><input type="number" min="0" step="0.5" data-experience-max value="${escapeHtml(requirement.maximum_years ?? "")}"></label>
        <label><span>Area</span><input data-experience-area value="${escapeHtml(requirement.area || "")}" placeholder="Example: data engineering"></label>
      </div>`).join("");
    const classifiedOptions = (digest.classified_requirements || []).map((requirement, index) => (
      `<label class="job-classified-edit-row"><input type="checkbox" data-classified-requirement-index="${index}" checked><span><strong>${escapeHtml(requirement.name)}</strong><small>${escapeHtml(requirement.text)}</small></span></label>`
    )).join("");
    elements.jobRequirementList.innerHTML = `<div class="job-requirement-editor">
      <div class="job-requirement-editor-grid">
        <label><span>Required skills</span><textarea id="job-edit-required-skills" rows="3">${escapeHtml((job.required_skills || []).join("\n"))}</textarea></label>
        <label><span>Required platforms</span><textarea id="job-edit-required-platforms" rows="3">${escapeHtml((job.required_platforms || []).join("\n"))}</textarea></label>
        <label><span>Required capabilities</span><textarea id="job-edit-required-capabilities" rows="3">${escapeHtml((job.required_capabilities || []).join("\n"))}</textarea></label>
        <label><span>Preferred skills</span><textarea id="job-edit-preferred-skills" rows="3">${escapeHtml((job.preferred_skills || []).join("\n"))}</textarea></label>
        <label class="wide"><span>Education requirements</span><textarea id="job-edit-education-requirements" rows="3">${escapeHtml((job.education_requirements || []).join("\n"))}</textarea></label>
      </div>
      <div class="job-experience-editor"><strong>Experience requirements</strong>${experienceRows}</div>
      ${classifiedOptions ? `<div class="job-classified-editor"><strong>Parser-classified requirements to keep</strong>${classifiedOptions}</div>` : ""}
    </div>`;
  } else {
    elements.jobRequirementList.innerHTML = requirementHtml.length
      ? `<ul>${requirementHtml.join("")}</ul>`
      : '<span class="empty-state">No explicit requirement detected.</span>';
  }

  const responsibilities = job.responsibilities || [];
  elements.jobResponsibilityList.innerHTML = responsibilities.length
    ? responsibilities.map((responsibility) => `<li translate="no">${escapeHtml(responsibility)}</li>`).join("")
    : '<li class="empty-state">No responsibility section detected.</li>';

  const coverage = digest.analysis_coverage;
  if (coverage) {
    const reviewItems = coverage.review_items || [];
    elements.jobCoverageSection.classList.remove("hidden");
    elements.jobCoverageSection.open = false;
    elements.jobCoverageSummary.textContent = reviewItems.length
      ? `${reviewItems.length} line${reviewItems.length === 1 ? "" : "s"} need review`
      : "No unstructured relevant lines";
    const metrics = [
      ["Source", coverage.source_lines],
      ["Structured", coverage.structured_lines],
      ["Needs review", coverage.needs_review_lines],
      ["Context", coverage.context_lines],
    ];
    elements.jobCoverageMetrics.innerHTML = metrics.map(([label, value]) => (
      `<div><strong>${Number(value || 0).toLocaleString()}</strong><span>${escapeHtml(label)}</span></div>`
    )).join("");
    elements.jobReviewLineList.innerHTML = reviewItems.length
      ? reviewItems.map((item) => `
          <div class="job-review-line">
            <div><span>Line ${Number(item.line)}</span><strong>${escapeHtml(item.section)}</strong></div>
            <p translate="no">${escapeHtml(item.text)}</p>
          </div>`).join("")
      : '<span class="empty-state">All relevant section lines produced at least one structured fact.</span>';
  } else {
    elements.jobCoverageSection.classList.add("hidden");
    elements.jobCoverageMetrics.innerHTML = "";
    elements.jobReviewLineList.innerHTML = "";
  }

  const unresolved = digest.unresolved_fields || [];
  elements.jobReviewNotice.textContent = unresolved.length
    ? `Needs review: ${unresolved.join(", ")}. Open the YAML draft before marking it ready.`
    : "No required identity fields are unresolved. Manual review is still required before ready status.";
}

function renderJobs() {
  if (!state.jobs.length) {
    elements.jobDraftSelect.innerHTML = '<option value="">No saved job descriptions</option>';
    elements.jobDraftSelect.disabled = true;
    jobDraftCombobox.setOptions([]);
    jobDraftCombobox.setDisabled(true);
    elements.loadJobSourceButton.disabled = true;
    elements.reanalyzeJobButton.disabled = true;
    elements.jobSavedCount.textContent = "0 saved";
    elements.loadMoreJobsButton.classList.add("hidden");
    elements.jobSelectionMeta.textContent = "Save a job description to create the first local record.";
    renderJobLibraryApplication();
    renderJobWorkspaceMaterials();
    renderApplicationLedger();
    elements.jobApplicationSection.classList.add("hidden");
    elements.jobDigestSection.classList.add("hidden");
    elements.jobAssessment.classList.add("hidden");
    elements.jobInstructionChecklist.classList.add("hidden");
    elements.jobContactCandidates.classList.add("hidden");
    return;
  }
  if (!state.jobs.some((job) => job.id === state.selectedJobId)) {
    state.selectedJobId = state.jobs[0].id;
  }

  elements.jobDraftSelect.disabled = false;
  jobDraftCombobox.setDisabled(false);
  elements.loadJobSourceButton.disabled = false;
  const loadedLabel = state.jobs.length < state.jobsTotal
    ? `${state.jobs.length.toLocaleString()} loaded / ${state.jobsTotal.toLocaleString()} saved`
    : `${state.jobsTotal.toLocaleString()} saved`;
  elements.jobSavedCount.textContent = loadedLabel;
  elements.loadMoreJobsButton.classList.toggle("hidden", !state.jobsNextCursor);
  elements.loadMoreJobsButton.textContent = `Load more (${state.jobs.length}/${state.jobsTotal})`;
  elements.jobDraftSelect.innerHTML = state.jobs.map((job) => {
    const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
    const status = job.valid ? job.status : "invalid";
    return '<option value="' + escapeHtml(job.id) + '">' + escapeHtml(role + " - " + status) + "</option>";
  }).join("");
  elements.jobDraftSelect.value = state.selectedJobId;
  jobDraftCombobox.setOptions(state.jobs.map((job) => ({
    value: job.id,
    label: `${[job.company, job.title].filter(Boolean).join(" / ") || job.id} - ${job.valid ? job.status : "invalid"}`,
  })));
  jobDraftCombobox.setValue(state.selectedJobId);

  const selectedJob = state.jobs.find((job) => job.id === state.selectedJobId);
  const selectedStatus = selectedJob.valid ? selectedJob.status : "invalid";
  const analysisStatus = selectedJob.analysis_stale ? "analysis update available" : "analysis current";
  elements.reanalyzeJobButton.disabled = selectedStatus !== "draft";
  elements.reanalyzeJobButton.textContent = selectedStatus === "draft" ? "Refresh analysis" : "Analysis locked";
  elements.jobSelectionMeta.textContent = selectedStatus + " | " + analysisStatus + " | "
    + Number(selectedJob.source_chars || 0).toLocaleString()
    + " characters | updated " + formatTime(selectedJob.modified_at);
  renderJobLibraryApplication();
  renderJobWorkspaceMaterials();
  renderApplicationLedger();
  renderJobApplication();
  renderJobDigest();
  renderJobInstructions();
  renderJobContactCandidates();
  renderJobAssessment();
  setJobWorkspaceTab(state.jobWorkspaceTab);
}

async function selectJob(jobId, { scroll = false, autoReanalyze = true } = {}) {
  if (!state.jobs.some((job) => job.id === jobId)) return;
  state.selectedJobId = jobId;
  state.jobSourcePreview = null;
  state.jobAssessment = null;
  state.jobAssessmentError = "";
  state.jobInstructions = null;
  state.jobInstructionsError = "";
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "";
  hideJobEditForm();
  renderJobs();
  const selectedJob = state.jobs.find((job) => job.id === jobId);
  if (autoReanalyze && api.capabilities.job_reanalysis_revision !== true && selectedJob?.status === "draft" && selectedJob.analysis_stale) {
    await reanalyzeSelectedJob({ automatic: true });
  } else {
    await Promise.all([
      loadSelectedJobAssessment(),
      loadSelectedJobInstructions(),
    ]);
  }
  if (state.activeView === "job-library") {
    await loadSelectedJobApplication({ includeSession: false });
  }
  if (scroll) {
    elements.jobDigestSection.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function updateJobCharacterCount() {
  const count = elements.jobSourceText.value.length;
  elements.jobCharacterCount.textContent = `${count.toLocaleString()} characters`;
}

function mergeCollection(existing, incoming, key) {
  const merged = new Map(existing.map((item) => [item[key], item]));
  incoming.forEach((item) => merged.set(item[key], item));
  return [...merged.values()];
}

async function loadJobs({ force = false, autoReanalyze = true } = {}) {
  if (!jobsLoaded || force) {
    const jobsPage = await api.getJobs();
    state.jobs = jobsPage.items || [];
    state.jobsTotal = Number(jobsPage.total || 0);
    state.jobsNextCursor = jobsPage.next_cursor || null;
    jobsLoaded = true;
  }
  renderJobs();
  const selectedJob = state.jobs.find((job) => job.id === state.selectedJobId);
  if (state.activeView === "job-library" && autoReanalyze && api.capabilities.job_reanalysis_revision !== true && selectedJob?.status === "draft" && selectedJob.analysis_stale) {
    await reanalyzeSelectedJob({ automatic: true });
    if (state.jobApplication?.job_id !== state.selectedJobId) {
      await loadSelectedJobApplication({ includeSession: false });
    }
    return;
  }
  if (state.activeView === "job-library") {
    await Promise.all([
      loadSelectedJobAssessment(),
      loadSelectedJobInstructions(),
    ]);
    await loadSelectedJobApplication({ includeSession: false });
  }
  if (state.activeView === "applications") await loadSelectedJobApplication();
}

async function loadResumeCollections({ force = false } = {}) {
  if (!resumeCollectionsLoaded || force) {
    const [sourcesPage, extractionsPage] = await Promise.all([
      api.getSources({ limit: 50 }),
      api.getExtractions({ limit: 100 }),
    ]);
    state.sources = sourcesPage.items || [];
    state.sourcesTotal = Number(sourcesPage.total || 0);
    state.sourcesNextCursor = sourcesPage.next_cursor || null;
    state.extractions = extractionsPage.items || [];
    state.extractionsTotal = Number(extractionsPage.total || 0);
    state.extractionsNextCursor = extractionsPage.next_cursor || null;
    resumeCollectionsLoaded = true;
  }
  renderSources();
  renderExtractions();
  if (state.activeView === "review" && state.reviewDetail) renderReview();
}

async function loadState({ autoReanalyze = true } = {}) {
  await Promise.all([
    loadJobs({ force: true, autoReanalyze }),
    loadResumeCollections({ force: true }),
  ]);
}

async function ensureViewData(viewName, { force = false } = {}) {
  if (["vault", "projects", "education"].includes(viewName)) {
    return withVaultLoading(viewName, async () => {
      if (!await ensureVaultSetup()) {
        if (state.activeView === viewName) switchView("profile");
        return;
      }
      if (viewName === "vault") await loadVaultExperiences({ refresh: force });
      else if (viewName === "projects") await loadVaultProjects({ refresh: force });
      else await loadVaultEducation({ refresh: force });
    });
  }
  if (viewName === "profile") {
    if (!await ensureVaultSetup()) return;
    await Promise.all([
      loadVaultProfile({ refresh: force }),
      loadEligibilityProfile(),
      loadHardBlockerPolicy(),
      loadApplicationHelperConfiguration(),
      loadDocuments(),
    ]);
  } else if (viewName === "skills") {
    await loadSkillsHub({ force });
  } else if (viewName === "documents") {
    await loadDocuments();
  } else if (viewName === "contacts") {
    await loadContacts();
  } else if (viewName === "demand-gaps") {
    await Promise.all([
      loadDemandGaps({ force: true }),
      loadJobs({ force, autoReanalyze: false }),
    ]);
    renderDemandGaps();
  } else if (viewName === "job-feed") {
    await loadDiscoveries();
  } else if (["job-library", "applications"].includes(viewName)) {
    await loadJobs({ force, autoReanalyze: viewName === "job-library" });
    if (viewName === "job-library") await loadResumeLibrary();
    if (viewName === "applications") {
      await Promise.all([
        loadApplicationEmailUpdates(),
        loadApplicationSessionReviews(),
      ]);
    }
  } else if (viewName === "application-materials") {
    await Promise.all([
      loadJobs({ force, autoReanalyze: false }),
      loadResumeLibrary(),
    ]);
    if (force && state.materialEditorJobId) {
      if (state.materialEditorTab === "resume") {
        await loadResumeMaterial({ force: true });
      } else {
        await loadCoverLetterMaterial({ force: true });
      }
    }
  } else if (viewName === "resume-library") {
    await loadResumeLibraryManagement({ force });
  } else if (viewName === "review" || (viewName === "intake"
    && elements.resumeIntentInputs.find((input) => input.checked)?.value !== "applications")) {
    await loadResumeCollections({ force });
  }
}

async function refreshActiveView() {
  try {
    await ensureViewData(state.activeView, { force: true });
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

async function loadMoreJobs() {
  if (!state.jobsNextCursor) return;
  elements.loadMoreJobsButton.disabled = true;
  try {
    const page = await api.getJobs({ cursor: state.jobsNextCursor });
    state.jobs = mergeCollection(state.jobs, page.items || [], "id");
    state.jobsTotal = Number(page.total || state.jobsTotal);
    state.jobsNextCursor = page.next_cursor || null;
    renderJobs();
  } catch (error) {
    elements.jobLibraryMessage.className = "form-message error";
    elements.jobLibraryMessage.textContent = formatRequestError(error);
  } finally {
    elements.loadMoreJobsButton.disabled = false;
  }
}

async function loadMoreSources() {
  if (!state.sourcesNextCursor) return;
  elements.loadMoreSourcesButton.disabled = true;
  try {
    const page = await api.getSources({ cursor: state.sourcesNextCursor, limit: 50 });
    state.sources = mergeCollection(state.sources, page.items || [], "name");
    state.sourcesTotal = Number(page.total || state.sourcesTotal);
    state.sourcesNextCursor = page.next_cursor || null;
    renderSources();
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  } finally {
    elements.loadMoreSourcesButton.disabled = false;
  }
}

function highlightedSkillBlock(block, mentions) {
  const matches = (mentions || [])
    .filter((mention) => mention.source_ref?.block_id === block.id)
    .filter((mention) => Number.isInteger(mention.source_ref?.start) && Number.isInteger(mention.source_ref?.end))
    .sort((left, right) => left.source_ref.start - right.source_ref.start);
  if (!matches.length) return escapeHtml(block.text);
  const registry = registrySkillIndex();
  let cursor = 0;
  const parts = [];
  matches.forEach((mention) => {
    const start = mention.source_ref.start;
    const end = mention.source_ref.end;
    if (start < cursor || end <= start || end > block.text.length) return;
    parts.push(escapeHtml(block.text.slice(cursor, start)));
    const origin = registry.get(mention.technology_id)?.origin || "core";
    parts.push(`<mark class="skill-highlight registry-skill-${escapeHtml(origin)}" title="${escapeHtml(mention.canonical_name)}">${escapeHtml(block.text.slice(start, end))}</mark>`);
    cursor = end;
  });
  parts.push(escapeHtml(block.text.slice(cursor)));
  return parts.join("");
}

function renderExtractionDetails(extraction) {
  elements.extractionPath.textContent = extraction.source_name || "Selected source";
  elements.metricStrip.innerHTML = Object.entries(extraction.counts).map(([label, value]) => (
    `<div class="metric"><strong>${value}</strong><span>${escapeHtml(label.replaceAll("_", " "))}</span></div>`
  )).join("");
  const registry = registrySkillIndex();
  elements.technologyList.innerHTML = extraction.technology_ids.length
    ? extraction.technology_ids.map((skillId) => {
      const skill = registry.get(skillId);
      const mention = (extraction.technology_mentions || []).find((item) => item.technology_id === skillId);
      const name = skill?.name || mention?.canonical_name || skillId;
      const origin = skill?.origin || "core";
      return `<span class="tag registry-skill-${escapeHtml(origin)}" title="${escapeHtml(mention?.source_text || name)}">${escapeHtml(name)}</span>`;
    }).join("")
    : '<span class="empty-state">No technologies detected.</span>';
  elements.sectionList.innerHTML = extraction.section_hints.length
    ? extraction.section_hints.map((section) => `
      <div class="plain-row">
        <code>${escapeHtml(section.source_ref.block_id)}</code>
        <span>${escapeHtml(section.section_type)}: ${escapeHtml(section.source_text)}</span>
      </div>`).join("")
    : '<div class="empty-state">No section hints detected.</div>';
  const documentBlocks = extraction.blocks.filter((block) => block.text);
  elements.documentBlockCount.textContent = `${documentBlocks.length} non-empty blocks`;
  elements.documentBlockList.innerHTML = documentBlocks.length
    ? documentBlocks.map((block) => `
      <div class="plain-row">
        <code>${escapeHtml(block.id)} / ${escapeHtml(block.kind)}${block.is_bullet ? " / bullet" : ""}</code>
        <span>${highlightedSkillBlock(block, extraction.technology_mentions)}</span>
      </div>`).join("")
    : '<div class="empty-state">No document text extracted.</div>';
  elements.bulletCount.textContent = `${extraction.bullet_candidates.length} candidates`;
  elements.bulletList.innerHTML = extraction.bullet_candidates.length
    ? extraction.bullet_candidates.map((bullet) => `
      <div class="bullet-row">
        <code>${escapeHtml(bullet.source_ref.block_id)}</code>
        <p>${escapeHtml(bullet.text)}</p>
      </div>`).join("")
    : '<div class="empty-state">No source bullets detected.</div>';
}

function clearExtractionDetails() {
  elements.extractionPath.textContent = "Loading source details…";
  elements.metricStrip.innerHTML = "";
  elements.technologyList.innerHTML = "";
  elements.sectionList.innerHTML = "";
  elements.documentBlockCount.textContent = "";
  elements.documentBlockList.innerHTML = "";
  elements.bulletCount.textContent = "";
  elements.bulletList.innerHTML = "";
}

async function loadExtraction(sourceId, { isCurrent = () => true } = {}) {
  if (!sourceId) return;
  if (!state.skillsRegistry) {
    await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
  }
  const extraction = await requestJson(`/api/extractions/${encodeURIComponent(sourceId)}`);
  if (!isCurrent()) return null;
  renderExtractionDetails(extraction);
  return extraction;
}

function setReviewMessage(message, error = false) {
  elements.reviewMessage.className = error ? "form-message error" : "form-message";
  elements.reviewMessage.textContent = message;
}

function syncReviewInspector() {
  const open = Boolean(
    state.reviewInspectorOpen
    && state.activeView === "review"
    && state.selectedEntityId
  );
  elements.reviewInspector.classList.toggle("open", open);
  elements.reviewInspectorBackdrop.hidden = !open;
  document.body.classList.toggle("review-inspector-open", open);
}

function setReviewInspectorOpen(open) {
  state.reviewInspectorOpen = Boolean(open);
  syncReviewInspector();
}

function currentReview() {
  return state.reviewDetail?.review || null;
}

const CATEGORY_LABELS = {
  profile: "Profile",
  summary: "Professional Summary",
  experience: "Work Experience",
  education: "Education",
  project: "Projects",
  skill: "Skills",
  certification: "Certifications",
  other: "Other",
};

const CATEGORY_GROUPS = [
  ["Candidate", ["profile", "summary"]],
  ["Career records", ["experience", "education", "project", "skill", "certification"]],
  ["Other", ["other"]],
];

const APPROVAL_REASON_LABELS = {
  name_match: "Name match",
  shared_contact: "Shared contact",
  singleton_profile_destination: "Profile destination",
  organization_match: "Organization match",
  shared_role_terms: "Shared role terms",
  shared_dates: "Shared dates",
  shared_technologies: "Shared technologies",
  institution_match: "Institution match",
  degree_match: "Degree match",
  major_match: "Major match",
  certification_name_match: "Certification name match",
  credential_match: "Credential match",
  project_name_match: "Project name match",
  skill_family_match: "Skill family match",
  shared_keywords: "Shared keywords",
};

const APPROVAL_FIELD_LABELS = {
  name: "Name",
  organization: "Organization",
  role: "Role",
  dates: "Dates",
  graduation: "Graduation",
  credential: "Credential",
  completed: "Completed",
  location: "Location",
  target_location: "Target location",
  work_authorization: "Work authorization",
  institution: "Institution",
  degree: "Degree",
  major: "Major",
  gpa: "GPA",
  honors: "Honors",
  skill_family: "Skill family",
  keywords: "Keywords",
  technologies: "Skills",
  category: "Category",
  summary: "Summary",
  content: "Content",
  contact: "Contact",
  email: "Email",
  phone: "Phone",
  linkedin: "LinkedIn",
  github: "GitHub",
  website: "Website",
  proof_points: "Proof points",
};

const APPROVAL_FIELD_ALIASES = {
  organization: "organization",
  company: "organization",
  title: "role",
  role: "role",
  date_text: "dates",
  dates: "dates",
  emails: "contact",
  phones: "contact",
  links: "contact",
  contact: "contact",
};

const APPROVAL_FIELD_ORDER = [
  "name",
  "organization",
  "role",
  "dates",
  "location",
  "contact",
  "institution",
  "degree",
  "major",
  "graduation",
  "credential",
  "completed",
  "gpa",
  "honors",
  "skill_family",
  "keywords",
  "technologies",
  "category",
  "summary",
  "content",
  "target_location",
  "work_authorization",
  "email",
  "phone",
  "linkedin",
  "github",
  "website",
  "proof_points",
];

const APPROVAL_COLLECTION_FIELDS = new Set(["honors", "keywords", "technologies", "proof_points"]);

function entityCategory(entity) {
  return Object.hasOwn(CATEGORY_LABELS, entity.entity_type)
    ? entity.entity_type
    : "other";
}

function entityIsQueued(entity) {
  return entity.review_state === "queued";
}
function entityLabel(entity) {
  if (entity.entity_type === "profile") {
    return entity.name || "Profile & contact";
  }
  if (entity.entity_type === "education") {
    return entity.institution || entity.degree || "Untitled education";
  }
  if (entity.entity_type === "certification") {
    return entity.credential || entity.name || "Untitled certification";
  }
  if (entity.entity_type === "project") {
    return entity.title || "Untitled project";
  }
  if (entity.entity_type === "summary") {
    return entity.name || "Professional Summary";
  }
  if (entity.entity_type === "skill") {
    return entity.name || "Untitled skill group";
  }
  const title = entity.title || "Untitled experience";
  return entity.organization ? entity.organization + " / " + title : title;
}

function approvalDecisions(review = currentReview()) {
  if (!review) return [];
  if (!Array.isArray(review.approval_decisions)) review.approval_decisions = [];
  return review.approval_decisions;
}

function approvalDecision(subjectKind, subjectId, review = currentReview()) {
  return approvalDecisions(review).find((decision) => (
    decision.subject_kind === subjectKind && decision.subject_id === subjectId
  )) || null;
}

function approvalPreviewEntity(subjectId) {
  return state.approvalPreview?.entities.find((entity) => entity.source_entity_id === subjectId) || null;
}

function approvalCandidateForDecision(entity, decision) {
  if (!entity || decision?.action !== "merge") return null;
  return entity.candidates.find((candidate) => candidate.target_id === decision.target_id) || null;
}

function candidateIsDeterministicMatch(candidate) {
  return candidate?.reasons?.some(
    (reason) => reason.code !== "singleton_profile_destination"
  ) || false;
}

function approvalCandidateField(candidate, field) {
  const value = candidate?.fields?.[field];
  if (Array.isArray(value)) return value.join(", ").trim();
  return String(value || "").trim();
}

function approvalCandidatePositionLabel(candidate) {
  const company = approvalCandidateField(candidate, "organization");
  const role = approvalCandidateField(candidate, "role");
  const dates = approvalCandidateField(candidate, "dates");
  return [company, role, dates].filter(Boolean).join(" | ")
    || String(candidate?.target_label || "Career Vault record");
}

function approvalCandidatesHtml(candidates) {
  if (!candidates?.length) return "";
  return `
    <section class="approval-candidate-panel" aria-label="Possible existing records">
      <div>
        <strong>${candidates.length === 1 ? "Possible existing record" : "Possible existing records"}</strong>
        <p>Advisory only. Compare the company, role, and dates, then choose Update, Create, or Do not add. Your saved decision will not change automatically.</p>
      </div>
      <ul class="approval-candidate-list">
        ${candidates.map((candidate) => {
          const company = approvalCandidateField(candidate, "organization");
          const role = approvalCandidateField(candidate, "role");
          const dates = approvalCandidateField(candidate, "dates");
          const details = [["Company", company], ["Role", role], ["Dates", dates]]
            .filter(([, value]) => value)
            .map(([label, value]) => `<span><strong>${escapeHtml(label)}:</strong> ${escapeHtml(value)}</span>`)
            .join("");
          return `<li><strong>${escapeHtml(candidate.target_label)}</strong>${details ? `<div>${details}</div>` : ""}</li>`;
        }).join("")}
      </ul>
    </section>`;
}

function defaultFieldResolutions(entity, candidate, action) {
  if (!entity || !["merge", "create"].includes(action)) return null;
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  const keys = new Set([...source.keys(), ...target.keys()]);
  const resolutions = {};
  keys.forEach((key) => {
    const sourceValues = source.get(key) || [];
    const targetValues = target.get(key) || [];
    if (action === "create") {
      resolutions[key] = sourceValues.length
        ? (APPROVAL_COLLECTION_FIELDS.has(key) ? "add_source" : "source")
        : "excluded";
      return;
    }
    const status = approvalComparisonStatus(sourceValues, targetValues);
    if (status.key === "exact") resolutions[key] = "same";
    else if (!sourceValues.length && targetValues.length) resolutions[key] = "vault";
    else resolutions[key] = "excluded";
  });
  return resolutions;
}

function setApprovalDecision(subjectKind, subjectId, rawValue) {
  const review = currentReview();
  if (!review) return;
  review.approval_decisions = approvalDecisions(review).filter((decision) => !(
    decision.subject_kind === subjectKind && decision.subject_id === subjectId
  ));
  if (rawValue) {
    const mergePrefix = "merge:";
    const action = rawValue.startsWith(mergePrefix) ? "merge" : rawValue;
    const targetId = rawValue.startsWith(mergePrefix) ? rawValue.slice(mergePrefix.length) : null;
    const entity = subjectKind === "entity" ? approvalPreviewEntity(subjectId) : null;
    const candidate = entity?.candidates.find((item) => item.target_id === targetId) || null;
    review.approval_decisions.push({
      subject_kind: subjectKind,
      subject_id: subjectId,
      action,
      target_id: targetId,
      field_resolutions: defaultFieldResolutions(entity, candidate, action),
    });
  }
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function setApprovalFieldResolution(subjectId, field, choice) {
  const decision = approvalDecision("entity", subjectId);
  const entity = approvalPreviewEntity(subjectId);
  if (!decision || !entity || !["merge", "create"].includes(decision.action)) return;
  const candidate = approvalCandidateForDecision(entity, decision);
  if (!decision.field_resolutions) {
    decision.field_resolutions = defaultFieldResolutions(entity, candidate, decision.action);
  }
  decision.field_resolutions[field] = choice === "source"
    ? (APPROVAL_COLLECTION_FIELDS.has(field) ? "add_source" : "source")
    : (candidate ? "vault" : "excluded");
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function useAllApprovalSourceFields(subjectId) {
  const decision = approvalDecision("entity", subjectId);
  const entity = approvalPreviewEntity(subjectId);
  if (!decision || !entity || !["merge", "create"].includes(decision.action)) return;
  const candidate = approvalCandidateForDecision(entity, decision);
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  if (!decision.field_resolutions) {
    decision.field_resolutions = defaultFieldResolutions(entity, candidate, decision.action);
  }
  source.forEach((sourceValues, field) => {
    const status = approvalComparisonStatus(sourceValues, target.get(field) || []);
    decision.field_resolutions[field] = status.key === "exact"
      ? "same"
      : (APPROVAL_COLLECTION_FIELDS.has(field) ? "add_source" : "source");
  });
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function pruneApprovalDecisions(review) {
  const entityIds = new Set(review.entities.map((entity) => entity.id));
  const categoryOnlyBlockIds = new Set(
    review.block_reviews
      .filter((item) => item.disposition === "categorized")
      .map((item) => item.source_ref.block_id)
  );
  review.approval_decisions = approvalDecisions(review).filter((decision) => (
    decision.subject_kind === "entity"
      ? entityIds.has(decision.subject_id)
      : categoryOnlyBlockIds.has(decision.subject_id)
  ));
}

function applyApprovalDefaults(preview) {
  const review = currentReview();
  if (!review) return 0;
  let changed = 0;
  preview.category_only_blocks.forEach((block) => {
    if (approvalDecision("block", block.block_id, review)) return;
    approvalDecisions(review).push({
      subject_kind: "block",
      subject_id: block.block_id,
      action: "exclude",
      target_id: null,
      field_resolutions: null,
    });
    changed += 1;
  });
  preview.entities.forEach((entity) => {
    const current = approvalDecision("entity", entity.source_entity_id, review);
    if (current) {
      if (["merge", "create"].includes(current.action) && current.field_resolutions == null) {
        current.field_resolutions = defaultFieldResolutions(
          entity,
          approvalCandidateForDecision(entity, current),
          current.action
        );
        changed += 1;
      }
      return;
    }
    const candidate = (
      entity.outcome === "possible_match"
      && entity.candidates.length === 1
      && candidateIsDeterministicMatch(entity.candidates[0])
    ) ? entity.candidates[0] : null;
    const action = candidate ? "merge" : entity.outcome === "new" ? "create" : "exclude";
    approvalDecisions(review).push({
      subject_kind: "entity",
      subject_id: entity.source_entity_id,
      action,
      target_id: candidate?.target_id || null,
      field_resolutions: defaultFieldResolutions(entity, candidate, action),
    });
    changed += 1;
  });
  if (changed) state.reviewDirty = true;
  return changed;
}

function selectedCardSuggestionCount() {
  const review = currentReview();
  if (!review || !state.selectedEntityId) return 0;
  return review.block_reviews.filter((item) => (
    item.disposition === "unassigned"
    && item.suggested_owner_ids.length === 1
    && item.suggested_owner_ids[0] === state.selectedEntityId
  )).length;
}

function reviewStats() {
  const review = currentReview();
  if (!review) return { assigned: 0, categorized: 0, unassigned: 0, ignored: 0, structural: 0, suggested: 0 };
  return review.block_reviews.reduce((counts, item) => {
    counts[item.disposition] += 1;
    if (item.suggested_owner_ids.length) counts.suggested += 1;
    return counts;
  }, { assigned: 0, categorized: 0, unassigned: 0, ignored: 0, structural: 0, suggested: 0 });
}

function renderReviewToolbar() {
  const review = currentReview();
  if (!review) {
    elements.reviewStatus.textContent = "Review not built";
    elements.reviewStatus.className = "status-warning";
    elements.reviewCounts.textContent = "Build a source-complete review before detecting cards.";
    elements.buildReviewButton.textContent = "Analyze resume";
    elements.buildReviewButton.disabled = !state.reviewSourceId;
    elements.suggestCardsButton.disabled = true;
    elements.acceptSuggestionsButton.disabled = true;
    elements.acceptCardButton.disabled = true;

    elements.saveDraftButton.disabled = true;
    elements.prepareApprovalButton.disabled = true;
    return;
  }
  const stats = reviewStats();
  const application = state.reviewDetail.approval_application;
  if (application) {
    elements.reviewStatus.textContent = "Applied to Career Vault";
    elements.reviewStatus.className = "status-success";
    elements.reviewCounts.textContent = `${application.changes.length} decisions applied / ${application.changed_files.length} Career Vault files changed`;
    elements.buildReviewButton.disabled = true;
    elements.suggestCardsButton.disabled = true;
    elements.acceptSuggestionsButton.disabled = true;
    elements.acceptCardButton.disabled = true;
    elements.saveDraftButton.disabled = true;
    elements.prepareApprovalButton.disabled = true;
    return;
  }
  const queuedCardCount = review.entities.filter(entityIsQueued).length;
  const activeCardCount = review.entities.length - queuedCardCount;
  const needsAttention = stats.unassigned + queuedCardCount;
  elements.reviewStatus.textContent = needsAttention ? "Needs attention" : "Review ready";
  elements.reviewStatus.className = needsAttention ? "status-warning" : "status-success";
  elements.reviewCounts.textContent = `${activeCardCount} detected records · ${needsAttention} need attention · ${stats.assigned} source blocks linked`;
  elements.buildReviewButton.textContent = review.entities.length ? "Refresh source blocks" : "Analyze resume";
  elements.buildReviewButton.disabled = false;
  elements.suggestCardsButton.disabled = false;
  elements.acceptSuggestionsButton.disabled = stats.suggested === 0;
  elements.acceptCardButton.disabled = selectedCardSuggestionCount() === 0;

  elements.saveDraftButton.disabled = !state.reviewDirty;
  const completion = liveCompletionReport();
  elements.prepareApprovalButton.disabled = !completion?.ready_for_preview || Boolean(state.approvalPreview);
}

function entityCardHtml(entity, counts, variant = "") {
  const isProfile = entity.entity_type === "profile";
  const isExperience = entity.entity_type === "experience";
  const isEducation = entity.entity_type === "education";
  const isCertification = entity.entity_type === "certification";
  const isSummary = entity.entity_type === "summary";
  const isSkill = entity.entity_type === "skill";
  const isProject = entity.entity_type === "project";
  let typeLabel = entity.entity_type || "Record";
  let primary = entity.name || entity.title || "Untitled record";
  let secondary = "";
  let detail = "";

  if (isProfile) {
    typeLabel = "Candidate";
    primary = entity.name || "Candidate not named";
    secondary = [
      entity.headline,
      entity.location,
      (entity.emails || [])[0],
    ].filter(Boolean).join(" / ") || "Contact details need review";
  } else if (isExperience) {
    typeLabel = "Experience";
    primary = entity.organization || "Organization not set";
    secondary = entity.title || "Role title not set";
    detail = entity.date_text || "";
  } else if (isEducation) {
    typeLabel = "Education";
    primary = entity.institution || "Institution not set";
    secondary = [entity.degree, entity.major].filter(Boolean).join(", ") || "Degree not set";
    detail = entity.date_text || "";
  } else if (isCertification) {
    typeLabel = "Certification";
    primary = entity.name || "Certification name not set";
    secondary = entity.credential || "Credential not set";
    detail = entity.date_text || "";
  } else if (isProject) {
    typeLabel = "Project";
    primary = entity.title || "Project title not set";
    secondary = (entity.items || [])[0] || "Project evidence not set";
    detail = entity.date_text || "";
  } else if (isSummary) {
    typeLabel = "Summary";
    primary = entity.name || "Professional Summary";
    secondary = entity.content || "Summary text not set";
  } else if (isSkill) {
    typeLabel = "Skill group";
    primary = entity.name || "Skill group not named";
    secondary = (entity.items || []).join(", ") || "Skills not set";
  } else {
    typeLabel = typeLabel.charAt(0).toUpperCase() + typeLabel.slice(1);
  }

  const classes = [
    "draft-card-button",
    variant,
    entity.id === state.selectedEntityId ? "active" : "",
  ].filter(Boolean).join(" ");
  return `
    <button class="${classes}" data-entity-id="${escapeHtml(entity.id)}" data-tooltip="Open ${escapeHtml(primary)} card details and source links." aria-pressed="${entity.id === state.selectedEntityId}">
      <span class="card-type">${escapeHtml(typeLabel)}</span>
      <strong>${escapeHtml(primary)}</strong>
      <span class="card-secondary">${escapeHtml(secondary)}</span>
      ${detail ? `<span class="card-detail">${escapeHtml(detail)}</span>` : ""}
      <span class="card-source-counts">${counts.assigned} linked / ${counts.suggested} to review</span>
    </button>`;
}

function renderEntityHierarchy() {
  const review = currentReview();
  const allEntities = review?.entities || [];
  const entities = allEntities.filter((entity) => !entityIsQueued(entity));
  const statsByEntity = new Map(
    entities.map((entity) => [entity.id, { assigned: 0, suggested: 0 }])
  );
  (review?.block_reviews || []).forEach((item) => {
    if (item.owner_id && statsByEntity.has(item.owner_id)) {
      statsByEntity.get(item.owner_id).assigned += 1;
    }
    item.suggested_owner_ids.forEach((entityId) => {
      if (statsByEntity.has(entityId)) statsByEntity.get(entityId).suggested += 1;
    });
  });

  const profiles = entities.filter((entity) => entity.entity_type === "profile");
  const experiences = entities.filter((entity) => entity.entity_type === "experience");
  const summaries = entities.filter((entity) => entity.entity_type === "summary");
  const education = entities.filter((entity) => entity.entity_type === "education");
  const certifications = entities.filter((entity) => entity.entity_type === "certification");
  const projects = entities.filter((entity) => entity.entity_type === "project");
  const skills = entities.filter((entity) => entity.entity_type === "skill");

  const candidateStatus = profiles.length ? "1 identified person" : "Candidate not detected";
  const summaryStatus = summaries.length === 1
    ? "1 source summary"
    : `${summaries.length} source summaries`;
  elements.candidateCount.textContent = `${candidateStatus} / ${summaryStatus}`;
  elements.candidateCard.innerHTML = profiles.length
    ? entityCardHtml(profiles[0], statsByEntity.get(profiles[0].id), "candidate-card-button")
    : '<div class="hierarchy-placeholder">No candidate profile detected.</div>';
  elements.candidateSummary.innerHTML = summaries.length
    ? summaries.map((entity) => (
      entityCardHtml(entity, statsByEntity.get(entity.id), "candidate-summary-card-button")
    )).join("")
    : "";

  elements.experienceCardCount.textContent = `${experiences.length} contexts`;
  elements.experienceCardRow.innerHTML = experiences.length
    ? experiences.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No work contexts detected yet.</div>';

  elements.educationCardCount.textContent = `${education.length} records`;
  elements.educationCardRow.innerHTML = education.length
    ? education.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No education detected yet.</div>';

  elements.certificationCardCount.textContent = `${certifications.length} records`;
  elements.certificationCardRow.innerHTML = certifications.length
    ? certifications.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No certifications detected yet.</div>';

  elements.projectCardCount.textContent = `${projects.length} records`;
  elements.projectCardRow.innerHTML = projects.length
    ? projects.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No projects detected yet.</div>';

  elements.skillCardCount.textContent = `${skills.length} groups`;
  elements.skillCardRow.innerHTML = skills.length
    ? skills.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No skill groups detected yet.</div>';
}

function normalizeReviewSkillName(value) {
  return String(value || "").trim().replace(/\s+/g, " ").toLocaleLowerCase("en-US");
}

function activeRegistryNameIndex() {
  const index = new Map();
  registrySkillIndex().forEach((skill) => {
    if (skill.archived) return;
    [skill.name, ...(skill.aliases || [])].forEach((value) => {
      index.set(normalizeReviewSkillName(value), skill);
    });
  });
  return index;
}

function reviewSkillGroupOptions(selectedId) {
  const groups = (state.skillsRegistry?.groups || []).filter((group) => !group.archived);
  const fallbackId = state.skillsRegistry?.default_custom_group_id || groups[0]?.id || "";
  const effectiveId = groups.some((group) => group.id === selectedId) ? selectedId : fallbackId;
  return [
    ...groups.map((group) => `<option value="${escapeHtml(group.id)}"${group.id === effectiveId ? " selected" : ""} translate="no">${escapeHtml(group.name)} · ${escapeHtml(group.origin)}</option>`),
    '<option value="__create_group__">+ Create a new group…</option>',
  ].join("");
}

function reviewSkillsEditorHtml(entity) {
  const skills = registrySkillIndex();
  const existingIds = [...new Set(entity.technologies || [])];
  const pending = entity.pending_skills || [];
  const optionValues = [];
  skills.forEach((skill) => {
    if (skill.archived) return;
    optionValues.push(skill.name, ...(skill.aliases || []));
  });
  const datalistId = `review-skill-options-${entity.id.replace(/[^A-Za-z0-9_-]/g, "-")}`;
  return `
    <section class="review-skills-editor" data-review-skills-entity="${escapeHtml(entity.id)}">
      <div class="review-skills-heading">
        <div><label for="review-skill-input-${escapeHtml(entity.id)}">Skills</label><p>Exact registry matches link now. Unknown labels remain proposals until final approval.</p></div>
        <span>${existingIds.length + pending.length} selected</span>
      </div>
      <div class="review-skill-pills">
        ${existingIds.map((skillId) => {
          const skill = skills.get(skillId);
          const name = skill?.name || skillId;
          const origin = skill?.origin || "unknown";
          const groupName = skill?.group?.name || "Missing registry reference";
          return `<span class="review-skill-pill registry-skill-${escapeHtml(origin)}" title="${escapeHtml(groupName)}"><span>${escapeHtml(name)}</span><button type="button" data-remove-review-skill-id="${escapeHtml(skillId)}" aria-label="Remove ${escapeHtml(name)}">&times;</button></span>`;
        }).join("")}
        ${existingIds.length || pending.length ? "" : '<span class="empty-state">No skills selected for this evidence card.</span>'}
      </div>
      <div class="review-skill-add-row">
        <input id="review-skill-input-${escapeHtml(entity.id)}" data-review-skill-input list="${escapeHtml(datalistId)}" maxlength="500" autocomplete="off" placeholder="Type a skill; use comma for several">
        <datalist id="${escapeHtml(datalistId)}">${[...new Set(optionValues)].map((value) => `<option value="${escapeHtml(value)}"></option>`).join("")}</datalist>
        <button type="button" class="secondary-button" data-add-review-skills>Add</button>
      </div>
      ${pending.length ? `<div class="review-pending-skills"><p class="section-kicker">New custom skills on approval</p>${pending.map((item, index) => `
        <div class="review-pending-skill" data-pending-skill-index="${index}">
          <span class="review-skill-pill registry-skill-custom"><span>${escapeHtml(item.name)}</span><button type="button" data-remove-pending-skill="${index}" aria-label="Remove ${escapeHtml(item.name)}">&times;</button></span>
          <label><span>Group</span><select data-pending-skill-group="${index}">${reviewSkillGroupOptions(item.primary_group_id)}</select></label>
        </div>`).join("")}</div>` : ""}
    </section>`;
}

function addReviewSkills(entity, rawValue) {
  const values = String(rawValue || "")
    .split(/[,;\n]+/)
    .map((value) => value.trim().replace(/\s+/g, " "))
    .filter(Boolean);
  if (!values.length) return false;
  entity.technologies = [...new Set(entity.technologies || [])];
  entity.pending_skills = entity.pending_skills || [];
  const names = activeRegistryNameIndex();
  let changed = false;
  values.forEach((value) => {
    if (value.length > 160) {
      setReviewMessage(`Skill labels must be 160 characters or fewer: ${value.slice(0, 40)}…`, true);
      return;
    }
    const normalized = normalizeReviewSkillName(value);
    const matched = names.get(normalized);
    if (matched) {
      if (!entity.technologies.includes(matched.id)) {
        entity.technologies.push(matched.id);
        changed = true;
      }
      const before = entity.pending_skills.length;
      entity.pending_skills = entity.pending_skills.filter(
        (item) => normalizeReviewSkillName(item.name) !== normalized
      );
      changed = changed || entity.pending_skills.length !== before;
      return;
    }
    const duplicate = entity.pending_skills.some(
      (item) => normalizeReviewSkillName(item.name) === normalized
    );
    if (duplicate) return;
    entity.pending_skills.push({
      name: value,
      primary_group_id: state.skillsRegistry?.default_custom_group_id || null,
    });
    changed = true;
  });
  if (changed) {
    markReviewDirty();
    renderEntityHierarchy();
    renderCardEditor();
  }
  return changed;
}

function renderCardEditor() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  const queued = Boolean(entity && entityIsQueued(entity));
  syncReviewInspector();
  elements.deleteCardButton.hidden = !entity || queued || entity.entity_type === "profile";
  elements.restoreCardButton.hidden = !queued;
  elements.ungroupCardButton.hidden = !queued;
  elements.acceptCardButton.hidden = !entity || queued;
  if (!entity) {
    elements.cardEditor.innerHTML = '<div class="review-placeholder">Select a detected card.</div>';
    return;
  }
  const suggestionCount = selectedCardSuggestionCount();
  elements.restoreCardButton.textContent = queued ? `Restore ${entityLabel(entity)}` : "Restore card";
  elements.acceptCardButton.textContent = suggestionCount
    ? `Link ${suggestionCount} source blocks`
    : "No links to review";
  elements.acceptCardButton.disabled = suggestionCount === 0;

  let fields;
  if (entity.entity_type === "profile") {
    fields = [
      ["name", "Name", entity.name],
      ["headline", "Professional headline", entity.headline],
      ["location", "Location", entity.location],
      ["emails", "Email", (entity.emails || []).join(", ")],
      ["phones", "Phone", (entity.phones || []).join(", ")],
      ["links", "LinkedIn / GitHub / website", (entity.links || []).join(", ")],
    ];
  } else if (entity.entity_type === "summary") {
    fields = [
      ["name", "Section name", entity.name],
      ["content", "Summary text", entity.content],
    ];
  } else if (entity.entity_type === "skill") {
    fields = [
      ["name", "Skill group", entity.name],
      ["items", "Skills", (entity.items || []).join(", ")],
      ["technologies", "Matched technology IDs", (entity.technologies || []).join(", ")],
    ];
  } else if (entity.entity_type === "project") {
    fields = [
      ["title", "Project title", entity.title],
      ["date_text", "Dates", entity.date_text],
      ["items", "Project evidence (one item per line)", (entity.items || []).join("\n")],
    ];
  } else if (entity.entity_type === "education") {
    fields = [
      ["institution", "Institution", entity.institution],
      ["degree", "Degree", entity.degree],
      ["major", "Major / field of study", entity.major],
      ["date_text", "Graduation / dates", entity.date_text],
      ["location", "Location", entity.location],
      ["gpa", "GPA", entity.gpa],
      ["honors", "Honors / awards", (entity.honors || []).join(", ")],
    ];
  } else if (entity.entity_type === "certification") {
    fields = [
      ["name", "Certification name / issuer", entity.name],
      ["credential", "Credential", entity.credential],
      ["date_text", "Completed", entity.date_text],
      ["items", "Proof points (one item per line)", (entity.items || []).join("\n")],
    ];
  } else {
    fields = [
      ["organization", "Organization", entity.organization],
      ["title", "Role title", entity.title],
      ["date_text", "Dates", entity.date_text],
      ["location", "Location", entity.location],
    ];
  }
  const multilineFields = new Set(["content", "items"]);
  const skillsEditor = ["experience", "project", "certification"].includes(entity.entity_type)
    ? reviewSkillsEditorHtml(entity)
    : "";
  elements.cardEditor.innerHTML = fields.map(([name, label, value]) => multilineFields.has(name) ? `
    <div class="editor-field">
      <label for="card-${escapeHtml(name)}">${escapeHtml(label)}</label>
      <textarea id="card-${escapeHtml(name)}" data-card-field="${escapeHtml(name)}">${escapeHtml(value)}</textarea>
    </div>` : `
    <div class="editor-field">
      <label for="card-${escapeHtml(name)}">${escapeHtml(label)}</label>
      <input id="card-${escapeHtml(name)}" data-card-field="${escapeHtml(name)}" value="${escapeHtml(value)}">
    </div>`).join("") + skillsEditor + `
    <div class="editor-field">
      <label for="card-user-note">Notes</label>
      <textarea id="card-user-note" data-card-field="user_note">${escapeHtml(entity.user_note || "")}</textarea>
    </div>
    <div class="editor-field">
      <label>Detection rule</label>
      <input value="${escapeHtml(entity.suggestion_rule || "Manual card")}" disabled>
    </div>`;
}

function classificationOptions(item) {
  const current = item.disposition === "ignored"
    ? "__ignored"
    : item.disposition === "structural"
      ? "__structural"
      : item.category || "";
  const categoryGroups = CATEGORY_GROUPS.map(([groupLabel, categories]) => `
    <optgroup label="${escapeHtml(groupLabel)}">
      ${categories.map((category) => `
        <option value="${escapeHtml(category)}"${current === category ? " selected" : ""}>${escapeHtml(CATEGORY_LABELS[category])}</option>
      `).join("")}
    </optgroup>`).join("");
  return `
    <option value=""${current === "" ? " selected" : ""}>Unclassified</option>
    ${categoryGroups}
    <optgroup label="Review decision">
      <option value="__structural"${current === "__structural" ? " selected" : ""}>Section marker</option>
      <option value="__ignored"${current === "__ignored" ? " selected" : ""}>Ignored</option>
    </optgroup>`;
}

function ownerOptions(item, entities) {
  const current = item.disposition === "categorized"
    ? "__category__"
    : item.disposition === "assigned" ? item.owner_id : "";
  const rootOption = item.category
    ? `<option value="__category__"${current === "__category__" ? " selected" : ""}>Keep directly under ${escapeHtml(CATEGORY_LABELS[item.category] || item.category)}</option>`
    : "";
  const categories = CATEGORY_GROUPS.flatMap(([, values]) => values);
  const groups = categories.map((category) => {
    if (item.category && category !== item.category) return "";
    const categoryEntities = entities.filter(
      (entity) => entityCategory(entity) === category
    );
    const activeEntities = categoryEntities.filter((entity) => !entityIsQueued(entity));
    const queuedEntities = categoryEntities.filter(entityIsQueued);
    const activeGroup = activeEntities.length ? `
      <optgroup label="${escapeHtml(CATEGORY_LABELS[category])} cards">
        ${activeEntities.map((entity) => `
          <option value="${escapeHtml(entity.id)}"${current === entity.id ? " selected" : ""}>${escapeHtml(entityLabel(entity))}</option>
        `).join("")}
      </optgroup>` : "";
    const queuedGroup = queuedEntities.length ? `
      <optgroup label="Queued ${escapeHtml(CATEGORY_LABELS[category])} cards">
        ${queuedEntities.map((entity) => `
          <option value="${escapeHtml(entity.id)}"${current === entity.id ? " selected" : ""}>${escapeHtml(entityLabel(entity))}</option>
        `).join("")}
      </optgroup>` : "";
    return activeGroup + queuedGroup;
  }).join("");
  return `<option value="">Needs card</option>${rootOption}${groups}`;
}

function blockReviewGroup(item) {
  if (item.disposition === "assigned") return "linked";
  if (item.disposition === "categorized") return "categoryOnly";
  if (item.disposition === "ignored") return "ignored";
  if (item.disposition === "structural") return "structural";
  if (item.suggested_owner_ids.length) return "suggested";
  if (item.category) return "categorized";
  return "unclassified";
}

function sourceBlockHtml(block, item, review, entityById) {
  const suggested = item.suggested_owner_ids
    .map((entityId) => entityById.get(entityId))
    .filter(Boolean)
    .map(entityLabel)
    .join(", ");
  const group = blockReviewGroup(item);
  return `
    <div class="review-source-block" data-block-id="${escapeHtml(block.id)}">
      <div class="block-meta">
        <span>${escapeHtml(block.id)} / ${escapeHtml(block.kind)}${block.is_bullet ? " / bullet" : ""}</span>
        <span>${escapeHtml(group)}</span>
      </div>
      <p class="block-text">${escapeHtml(block.text)}</p>
      <div class="block-assignment">
        <label class="block-select-field">
          <span>Type</span>
          <select class="category-select" data-block-id="${escapeHtml(block.id)}" aria-label="Type for ${escapeHtml(block.id)}">
            ${classificationOptions(item)}
          </select>
        </label>
        <label class="block-select-field">
          <span>Attach to</span>
          <select class="owner-select" data-block-id="${escapeHtml(block.id)}" aria-label="Owner for ${escapeHtml(block.id)}"${["ignored", "structural"].includes(item.disposition) ? " disabled" : ""}>
            ${ownerOptions(item, review.entities)}
          </select>
        </label>
        ${suggested ? `<span class="suggestion-label" title="${escapeHtml(suggested)}">Suggested: ${escapeHtml(suggested)}</span>` : ""}
      </div>
    </div>`;
}

function approvalIssuesForReview(review) {
  const assignedCounts = new Map();
  review.block_reviews.forEach((item) => {
    if (item.disposition === "assigned" && item.owner_id) {
      assignedCounts.set(item.owner_id, (assignedCounts.get(item.owner_id) || 0) + 1);
    }
  });
  const legacyRequiredFields = {
    profile: [["name", "name"]],
    experience: [["organization", "organization"], ["title", "title"]],
    education: [["institution", "institution"]],
    certification: [["name", "name"], ["credential", "credential"], ["date_text", "completion date"]],
    project: [["title", "title"]],
    skill: [["name", "skill family"], ["items", "skill items"]],
  };
  const granularRequiredFields = {
    experience: [["organization", "organization"], ["role", "role"], ["dates", "dates"]],
    education: [["institution", "institution"]],
    certification: [["name", "name"], ["credential", "credential"], ["completed", "completion date"]],
    project: [["name", "name"], ["summary", "summary"]],
    skill: [["skill_family", "skill family"], ["keywords", "skill items"]],
  };
  const issues = [];
  review.block_reviews
    .filter((item) => item.disposition === "categorized")
    .forEach((item) => {
      if (!approvalDecision("block", item.source_ref.block_id, review)) {
        issues.push({
          code: "MISSING_APPROVAL_DECISION",
          message: "Choose Do not add, or return to card review and attach this block.",
          entity_id: null,
          block_id: item.source_ref.block_id,
        });
      }
    });
  review.entities.filter((entity) => !entityIsQueued(entity)).forEach((entity) => {
    if (!assignedCounts.get(entity.id)) {
      issues.push({
        code: "ENTITY_WITHOUT_SOURCE_BLOCKS",
        message: "Active card has no assigned source blocks.",
        entity_id: entity.id,
      });
      return;
    }
    const decision = approvalDecision("entity", entity.id, review);
    if (!decision) {
      issues.push({
        code: "MISSING_APPROVAL_DECISION",
        message: "Choose how this reviewed card should be handled.",
        entity_id: entity.id,
      });
      return;
    }
    if (["source_only", "exclude"].includes(decision.action)) return;
    if (!legacyRequiredFields[entity.entity_type]) {
      issues.push({
        code: "UNSUPPORTED_APPROVAL_DESTINATION",
        message: `${CATEGORY_LABELS[entity.entity_type] || entity.entity_type} cannot be added to the Career Vault in this phase.`,
        entity_id: entity.id,
      });
      return;
    }
    if (decision.action !== "create") return;
    const missingLabels = decision.field_resolutions == null
      ? legacyRequiredFields[entity.entity_type]
        .filter(([field]) => {
          const value = entity[field];
          return Array.isArray(value) ? value.length === 0 : !String(value || "").trim();
        })
        .map(([, label]) => label)
      : (granularRequiredFields[entity.entity_type] || [])
        .filter(([field]) => !["source", "add_source"].includes(decision.field_resolutions[field]))
        .map(([, label]) => label);
    missingLabels.forEach((label) => {
      issues.push({
        code: "MISSING_APPROVAL_FIELD",
        message: `${CATEGORY_LABELS[entity.entity_type] || entity.entity_type} requires source ${label} before creating a Career Vault record.`,
        entity_id: entity.id,
      });
    });
  });
  return issues;
}
function liveCompletionReport() {
  const review = currentReview();
  if (!review) return null;
  const unresolved = review.block_reviews.filter(
    (item) => item.disposition === "unassigned"
  );
  const validationErrorCodes = state.reviewDetail?.completion?.validation_error_codes || [];
  const suggestedBlockCount = unresolved.filter(
    (item) => item.suggested_owner_ids.length > 0
  ).length;
  const categorizedBlockCount = unresolved.filter((item) => item.category).length;
  const queuedEntityIds = review.entities
    .filter(entityIsQueued)
    .map((entity) => entity.id);
  const status = validationErrorCodes.length
    ? "invalid"
    : unresolved.length || queuedEntityIds.length ? "incomplete" : "ready";
  const approvalIssues = approvalIssuesForReview(review);
  const readyForPreview = status === "ready";
  return {
    status,
    ready_for_preview: readyForPreview,
    ready_to_approve: readyForPreview && approvalIssues.length === 0,
    unresolved_block_count: unresolved.length,
    suggested_block_count: suggestedBlockCount,
    categorized_block_count: categorizedBlockCount,
    unclassified_block_count: unresolved.length - categorizedBlockCount,
    queued_entity_count: queuedEntityIds.length,
    queued_entity_ids: queuedEntityIds,
    unresolved_blocks: unresolved.map((item) => ({
      block_id: item.source_ref.block_id,
      category: item.category,
      suggested_owner_ids: item.suggested_owner_ids,
    })),
    validation_error_codes: validationErrorCodes,
    approval_issue_count: approvalIssues.length,
    approval_issues: approvalIssues,
  };
}

function renderCompletionGate() {
  const detail = state.reviewDetail;
  const review = currentReview();
  const completion = liveCompletionReport();
  if (!detail || !review || !completion) {
    elements.completionGate.hidden = true;
    return;
  }

  elements.completionGate.hidden = false;
  elements.completionGate.className = `completion-gate ${completion.status}`;
  const blocksById = new Map(detail.blocks.map((block) => [block.id, block]));
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));

  if (completion.status === "ready") {
    elements.completionStatus.textContent = completion.ready_to_approve
      ? "Ready for approval"
      : "Source review complete";
    elements.completionCounts.textContent = completion.ready_to_approve
      ? `${review.block_reviews.length} source decisions complete`
      : `${completion.approval_issue_count} cards need an approval choice`;
    elements.completionBlockList.innerHTML = completion.approval_issues.map((issue) => {
      const entity = entityById.get(issue.entity_id);
      return `
        <div class="completion-block-row approval-issue-row">
          <code>Approval choice</code>
          <span class="completion-block-text">${escapeHtml(entity ? entityLabel(entity) : blocksById.get(issue.block_id)?.text || issue.block_id || "Review")}</span>
          <span class="completion-suggestion">${escapeHtml(issue.message)}</span>
        </div>`;
    }).join("");
    return;
  }

  if (completion.status === "invalid") {
    elements.completionStatus.textContent = "Review has validation errors";
    elements.completionCounts.textContent = `${completion.validation_error_codes.length} errors`;
    elements.completionBlockList.innerHTML = completion.validation_error_codes.map((code) => `
      <div class="completion-block-row validation-error-row">
        <code>${escapeHtml(code)}</code>
        <span>Resolve this validation error before approval.</span>
      </div>`).join("");
    return;
  }

  elements.completionStatus.textContent = "Decisions still required";
  elements.completionCounts.textContent = `${completion.queued_entity_count} queued cards / ${completion.unresolved_block_count} unresolved blocks / ${completion.suggested_block_count} with suggestions`;
  const queuedRows = completion.queued_entity_ids.map((entityId) => {
    const entity = entityById.get(entityId);
    return `
      <div class="completion-block-row queued-completion-row">
        <code>CARD</code>
        <span class="completion-block-text">${escapeHtml(entity ? entityLabel(entity) : entityId)}</span>
        <span class="completion-suggestion">Restore or ungroup this card.</span>
      </div>`;
  }).join("");
  const blockRows = completion.unresolved_blocks.map((item) => {
    const block = blocksById.get(item.block_id);
    const suggestions = item.suggested_owner_ids
      .map((entityId) => entityById.get(entityId))
      .filter(Boolean)
      .map(entityLabel);
    const suggestionText = suggestions.length
      ? `Suggested: ${suggestions.join(", ")}`
      : "No automatic suggestion";
    return `
      <div class="completion-block-row">
        <code>${escapeHtml(item.block_id)}</code>
        <span class="completion-block-text">${escapeHtml(block?.text || "Source block unavailable")}</span>
        <span class="completion-suggestion${suggestions.length ? " has-suggestion" : ""}">
          ${item.category ? `<strong>${escapeHtml(CATEGORY_LABELS[item.category] || item.category)}</strong>` : ""}
          ${escapeHtml(suggestionText)}
        </span>
      </div>`;
  }).join("");
  elements.completionBlockList.innerHTML = queuedRows + blockRows;
}

function queuedCardHtml(entity, review, blocksById, entityById) {
  const owned = review.block_reviews.filter((item) => item.owner_id === entity.id);
  const category = entityCategory(entity);
  return `
    <details class="queued-card-item" data-queued-card-id="${escapeHtml(entity.id)}">
      <summary>
        <span class="queued-card-type">${escapeHtml(CATEGORY_LABELS[category] || category)}</span>
        <strong>${escapeHtml(entityLabel(entity))}</strong>
        <span>${owned.length} source ${owned.length === 1 ? "block" : "blocks"}</span>
      </summary>
      <div class="queued-card-body">
        <div class="queued-card-actions">
          <button class="secondary-button compact-button" data-edit-queued-card="${escapeHtml(entity.id)}" data-tooltip="Open this queued card in the details editor.">Edit details</button>
          <button class="primary-button compact-button" data-restore-card="${escapeHtml(entity.id)}" data-tooltip="Return this card to the active card hierarchy.">Restore card</button>
          <button class="text-button" data-ungroup-card="${escapeHtml(entity.id)}" data-tooltip="Remove this system-created card, but keep its source blocks for reassignment.">Ungroup card</button>
        </div>
        <div class="queued-source-blocks">
          ${owned.length ? owned.map((item) => {
            const block = blocksById.get(item.source_ref.block_id);
            return block ? sourceBlockHtml(block, item, review, entityById) : "";
          }).join("") : '<div class="review-placeholder">This card has no attached source blocks.</div>'}
        </div>
      </div>
    </details>`;
}
function renderReviewBlocks() {
  const detail = state.reviewDetail;
  const review = currentReview();
  if (!detail || !review) {
    elements.reviewBlockList.innerHTML = '<div class="review-placeholder">Build the review to inspect source blocks.</div>';
    return;
  }

  const reviewByBlock = new Map(
    review.block_reviews.map((item) => [item.source_ref.block_id, item])
  );
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));
  const blocksById = new Map(detail.blocks.map((block) => [block.id, block]));
  const queuedEntities = review.entities.filter(entityIsQueued);
  const queuedEntityIds = new Set(queuedEntities.map((entity) => entity.id));
  const visibleQueuedEntities = state.blockFilter === "all"
    ? queuedEntities
    : state.blockFilter === "current"
      ? queuedEntities.filter((entity) => entity.id === state.selectedEntityId)
      : [];
  const groups = {
    suggested: [],
    categorized: [],
    categoryOnly: [],
    unclassified: [],
    linked: [],
    ignored: [],
    structural: [],
  };

  detail.blocks.forEach((block) => {
    const item = reviewByBlock.get(block.id);
    if (!item || (item.owner_id && queuedEntityIds.has(item.owner_id))) return;
    const group = blockReviewGroup(item);
    const belongsToCurrent = Boolean(state.selectedEntityId) && (
      item.owner_id === state.selectedEntityId
      || item.suggested_owner_ids.includes(state.selectedEntityId)
    );
    if (state.blockFilter === "all" && group === "structural") return;
    if (state.blockFilter === "current" && !belongsToCurrent) return;
    if (state.blockFilter === "unassigned" && group !== "unclassified") return;
    if (state.blockFilter === "categorized" && group !== "categorized") return;
    if (state.blockFilter === "suggested" && group !== "suggested") return;
    if (state.blockFilter === "structural" && group !== "structural") return;
    groups[group].push({ block, item });
  });

  const definitions = [
    ["suggested", "Needs review"],
    ["categorized", "Needs card"],
    ["categoryOnly", "Category only"],
    ["unclassified", "Unclassified"],
    ["linked", "Linked"],
    ["ignored", "Ignored"],
    ["structural", "Section markers"],
  ];
  const sections = definitions
    .filter(([group]) => groups[group].length)
    .map(([group, label]) => `
      <section class="source-block-group" data-review-group="${group}">
        <div class="queue-group-heading">
          <h3>${label}</h3>
          <span>${groups[group].length}</span>
        </div>
        ${groups[group].map(({ block, item }) => (
          sourceBlockHtml(block, item, review, entityById)
        )).join("")}
      </section>`);

  const queuedSection = visibleQueuedEntities.length ? `
    <section class="queued-card-group">
      <div class="queue-group-heading">
        <h3>Cards needing review</h3>
        <span>${visibleQueuedEntities.length}</span>
      </div>
      ${visibleQueuedEntities.map((entity) => (
        queuedCardHtml(entity, review, blocksById, entityById)
      )).join("")}
    </section>` : "";
  const rendered = [queuedSection, ...sections].filter(Boolean);
  elements.reviewBlockList.innerHTML = rendered.length
    ? rendered.join("")
    : '<div class="review-placeholder">No cards or blocks match this view.</div>';
}

function approvalFieldKey(field) {
  return APPROVAL_FIELD_ALIASES[field] || field;
}

function approvalValueItems(value) {
  if (Array.isArray(value)) return value.flatMap(approvalValueItems);
  const rendered = String(value || "").trim();
  return rendered ? [rendered] : [];
}

function approvalFieldMap(fields) {
  const mapped = new Map();
  Object.entries(fields || {}).forEach(([field, value]) => {
    const key = approvalFieldKey(field);
    const values = approvalValueItems(value);
    if (!values.length) return;
    const existing = mapped.get(key) || [];
    values.forEach((item) => {
      if (!existing.includes(item)) existing.push(item);
    });
    mapped.set(key, existing);
  });
  return mapped;
}

function normalizeApprovalValue(value) {
  return String(value || "")
    .normalize("NFKD")
    .toLocaleLowerCase("en-US")
    .replace(/[\u2010-\u2015]/g, "-")
    .replace(/[_/\\|,.;:()[\]{}]+/g, " ")
    .replace(/[^a-z0-9+#-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function approvalComparisonStatus(sourceValues, targetValues) {
  if (!sourceValues.length) return { key: "target-only", label: "Vault only" };
  if (!targetValues.length) return { key: "source-only", label: "Source only" };
  const source = [...new Set(sourceValues.map(normalizeApprovalValue).filter(Boolean))];
  const target = [...new Set(targetValues.map(normalizeApprovalValue).filter(Boolean))];
  if (
    source.length === target.length
    && source.every((value) => target.includes(value))
  ) {
    return { key: "exact", label: "Exact" };
  }
  const phrasesOverlap = source.some((left) => target.some((right) => (
    left.length > 2 && right.length > 2 && (left.includes(right) || right.includes(left))
  )));
  const sourceTokens = new Set(source.flatMap((value) => value.split(" ")).filter((value) => value.length > 1));
  const targetTokens = new Set(target.flatMap((value) => value.split(" ")).filter((value) => value.length > 1));
  const tokensOverlap = [...sourceTokens].some((value) => targetTokens.has(value));
  if (phrasesOverlap || tokensOverlap) return { key: "partial", label: "Partial overlap" };
  return { key: "different", label: "Different" };
}

function approvalFieldValueHtml(values) {
  return values.length
    ? escapeHtml(values.join(", "))
    : '<span class="approval-missing-value">Not present</span>';
}

function approvalComparisonRowsHtml(entity, candidate, decision) {
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  const keys = [...new Set([...source.keys(), ...target.keys()])].sort((left, right) => {
    const leftIndex = APPROVAL_FIELD_ORDER.indexOf(left);
    const rightIndex = APPROVAL_FIELD_ORDER.indexOf(right);
    if (leftIndex === -1 && rightIndex === -1) return left.localeCompare(right);
    if (leftIndex === -1) return 1;
    if (rightIndex === -1) return -1;
    return leftIndex - rightIndex;
  });
  if (!keys.length) return '<div class="approval-empty">No populated fields</div>';
  const editable = ["merge", "create"].includes(decision?.action);
  return keys.map((key) => {
    const sourceValues = source.get(key) || [];
    const targetValues = target.get(key) || [];
    const status = approvalComparisonStatus(sourceValues, targetValues);
    const resolution = decision?.field_resolutions?.[key] || (status.key === "exact" ? "same" : "excluded");
    const sourceSelected = ["source", "add_source"].includes(resolution);
    const vaultSelected = ["vault", "excluded"].includes(resolution);
    const exact = status.key === "exact";
    const sourceAction = APPROVAL_COLLECTION_FIELDS.has(key)
      ? (candidate ? "Add source values" : "Include values")
      : (candidate ? "Use source" : "Include");
    const vaultAction = candidate ? "Keep Career Vault" : "Do not add";
    return `
      <div class="approval-diff-row approval-diff-${escapeHtml(status.key)}">
        <strong class="approval-diff-field">${escapeHtml(APPROVAL_FIELD_LABELS[key] || key.replaceAll("_", " "))}</strong>
        <button type="button"
          class="approval-field-choice${sourceSelected ? " is-selected" : ""}${exact ? " is-same" : ""}"
          data-approval-field-subject="${escapeHtml(entity.source_entity_id)}"
          data-approval-field="${escapeHtml(key)}"
          data-approval-field-choice="source"
          aria-pressed="${sourceSelected || exact}"
          ${!editable || !sourceValues.length || exact ? "disabled" : ""}>
          <span>${approvalFieldValueHtml(sourceValues)}</span>
          <small>${exact ? "Same" : sourceAction}</small>
        </button>
        <button type="button"
          class="approval-field-choice${vaultSelected ? " is-selected" : ""}${exact ? " is-same" : ""}"
          data-approval-field-subject="${escapeHtml(entity.source_entity_id)}"
          data-approval-field="${escapeHtml(key)}"
          data-approval-field-choice="vault"
          aria-pressed="${vaultSelected || exact}"
          ${!editable || exact ? "disabled" : ""}>
          <span>${approvalFieldValueHtml(targetValues)}</span>
          <small>${exact ? "Same" : vaultAction}</small>
        </button>
        <span class="approval-diff-status">${escapeHtml(status.label)}</span>
      </div>`;
  }).join("");
}

function approvalSourceBlocksHtml(blocks) {
  if (!blocks?.length) return '<div class="approval-empty">No linked source blocks</div>';
  return `
    <ol class="approval-source-blocks">
      ${blocks.map((block) => `
        <li>
          <div><code>${escapeHtml(block.block_id)}</code><span>${escapeHtml(block.kind)}</span></div>
          <p>${escapeHtml(block.text)}</p>
        </li>`).join("")}
    </ol>`;
}

function approvalDecisionValue(decision) {
  if (!decision) return "";
  if (["source_only", "exclude"].includes(decision.action)) return "exclude";
  return decision.action === "merge" ? `merge:${decision.target_id || ""}` : decision.action;
}

function approvalDecisionSelectHtml(subjectKind, subjectId, options, helpText, showUseAll = false) {
  const current = approvalDecisionValue(approvalDecision(subjectKind, subjectId));
  const renderedOptions = options.map(([value, label]) => `
    <option value="${escapeHtml(value)}"${current === value ? " selected" : ""}>${escapeHtml(label)}</option>`).join("");
  return `
    <section class="approval-decision-panel">
      <label>
        <span>Career Vault action</span>
        <select
          class="approval-decision-select"
          data-approval-subject-kind="${escapeHtml(subjectKind)}"
          data-approval-subject-id="${escapeHtml(subjectId)}"
        >${renderedOptions}</select>
      </label>
      ${showUseAll ? `<button type="button" class="secondary approval-use-all" data-approval-use-all="${escapeHtml(subjectId)}">Use all source fields</button>` : ""}
      <p>${escapeHtml(helpText)}</p>
    </section>`;
}

function approvalCategoryOnlyHtml(blocks) {
  if (!blocks?.length) return "";
  return `
    <article class="approval-item approval-unsupported approval-category-only">
      <header class="approval-item-heading">
        <div><span>Category-only content</span><h3>${blocks.length} source ${blocks.length === 1 ? "block" : "blocks"}</h3></div>
        <strong>Needs a card</strong>
      </header>
      <div class="approval-category-blocks">
        ${blocks.map((block) => `
          <section class="approval-category-block">
            ${approvalDecisionSelectHtml(
              "block", block.block_id, [["exclude", "Do not add to Career Vault"]],
              "Return to card review to attach this block before adding it."
            )}
            ${approvalSourceBlocksHtml([block])}
          </section>`).join("")}
      </div>
    </article>`;
}

function approvalTargetHtml(candidate, entity, decision) {
  const reasons = candidate?.reasons?.length
    ? candidate.reasons.map((reason) => `
      <li><strong>${escapeHtml(APPROVAL_REASON_LABELS[reason.code] || reason.code.replaceAll("_", " "))}</strong>
      ${reason.values.length ? `<span>${escapeHtml(reason.values.join(", "))}</span>` : ""}</li>`).join("")
    : "";
  return `
    <section class="approval-target">
      <div class="approval-target-heading">
        <strong>${candidate ? escapeHtml(candidate.target_label) : "New Career Vault record"}</strong>
        ${candidate ? `<code>${escapeHtml(candidate.target_id)}</code>` : ""}
      </div>
      <div class="approval-diff-grid">
        <div class="approval-diff-header">
          <span>Field</span><strong>Reviewed source</strong><strong>Career Vault</strong><span>Comparison</span>
        </div>
        ${approvalComparisonRowsHtml(entity, candidate, decision)}
      </div>
      ${reasons ? `<ul class="approval-reasons">${reasons}</ul>` : ""}
      <details class="approval-source-details">
        <summary>Source evidence (${entity.source_blocks.length})</summary>
        ${approvalSourceBlocksHtml(entity.source_blocks)}
      </details>
    </section>`;
}

function approvalEntityHtml(entity) {
  const hasDeterministicMatch = entity.candidates.some(candidateIsDeterministicMatch);
  const displayOutcome = entity.outcome === "possible_match" && !hasDeterministicMatch
    ? "no_match"
    : entity.outcome;
  const outcomeLabels = { possible_match: "Possible existing record", no_match: "No identity match", new: "New card", unsupported: "Not supported" };
  const decision = approvalDecision("entity", entity.source_entity_id);
  const candidate = approvalCandidateForDecision(entity, decision);
  const decisionOptions = [];
  if (entity.outcome === "possible_match") {
    entity.candidates.forEach((item) => {
      decisionOptions.push([`merge:${item.target_id}`, `Update ${approvalCandidatePositionLabel(item)}`]);
    });
    if (entity.entity_type !== "profile") decisionOptions.push(["create", "Create new Career Vault record"]);
  } else if (entity.outcome === "new") {
    decisionOptions.push(["create", "Create new Career Vault record"]);
  }
  decisionOptions.push(["exclude", "Do not add to Career Vault"]);
  const editable = ["merge", "create"].includes(decision?.action);
  const targetContent = editable
    ? approvalTargetHtml(candidate, entity, decision)
    : `<div class="approval-not-added"><strong>Not selected for Career Vault</strong>
        <details class="approval-source-details"><summary>Source evidence (${entity.source_blocks.length})</summary>${approvalSourceBlocksHtml(entity.source_blocks)}</details></div>`;
  const helpText = entity.outcome === "possible_match"
    ? "This is an advisory default. Choose Update, Create, or Do not add. An existing saved decision is never changed automatically."
    : decision?.action === "merge"
      ? "Career Vault values remain unchanged until a source field is selected."
      : decision?.action === "create"
        ? "Select the source fields that belong in the new record."
        : "This reviewed card will remain only in the intake history.";
  return `
    <article class="approval-item approval-${escapeHtml(displayOutcome)}">
      <header class="approval-item-heading">
        <div><span>${escapeHtml(CATEGORY_LABELS[entity.entity_type] || entity.entity_type)}</span><h3>${escapeHtml(entity.source_label)}</h3></div>
        <strong>${escapeHtml(outcomeLabels[displayOutcome] || displayOutcome)}</strong>
      </header>
      ${entity.outcome === "possible_match" ? approvalCandidatesHtml(entity.candidates) : ""}
      ${approvalDecisionSelectHtml(
        "entity", entity.source_entity_id, decisionOptions, helpText, editable
      )}
      <div class="approval-comparison">${targetContent}</div>
    </article>`;
}
function renderApprovalPreview() {
  const preview = state.approvalPreview;
  const visible = Boolean(preview);
  elements.approvalPreview.hidden = !visible;
  elements.entityHierarchy.hidden = visible;
  elements.reviewWorkspace.hidden = visible;
  if (!preview) return;

  const counts = preview.entities.reduce((result, entity) => {
    const outcome = entity.outcome === "possible_match" && !entity.candidates.some(candidateIsDeterministicMatch)
      ? "no_match"
      : entity.outcome;
    result[outcome] = (result[outcome] || 0) + 1;
    return result;
  }, {});
  elements.approvalEngine.textContent = `${preview.engine.kind} / ${preview.engine.name} v${preview.engine.version}`;
  elements.approvalPreviewSummary.innerHTML = `
    <strong>${preview.entities.length} reviewed cards</strong>
    <span>${counts.possible_match || 0} possible existing records</span>
    <span>${counts.no_match || 0} no identity match</span>
    <span>${counts.new || 0} new</span>
    <span>${counts.unsupported || 0} not promotable</span>
    <span>${preview.category_only_blocks.length} category-only</span>
    <code>review ${escapeHtml(preview.review_revision.slice(0, 15))}...</code>
    <code>vault ${escapeHtml(preview.vault_revision.slice(0, 15))}...</code>`;
  const application = state.reviewDetail?.approval_application;
  elements.saveApprovalDecisionsButton.disabled = !state.reviewDirty || Boolean(application);
  elements.applyApprovalButton.disabled = (
    Boolean(application)
    || state.reviewDirty
    || !hasCurrentIntakeApprovalPreview(preview, state.reviewDetail)
    || !liveCompletionReport()?.ready_to_approve
  );
  elements.applyApprovalButton.textContent = application ? "Applied" : "Apply to Career Vault";
  elements.approvalPreviewList.innerHTML = preview.entities.map(approvalEntityHtml).join("")
    + approvalCategoryOnlyHtml(preview.category_only_blocks);
}

async function prepareApprovalPreview() {
  const completion = liveCompletionReport();
  const request = currentReviewRequest();
  if (!completion?.ready_for_preview || !request) {
    setReviewMessage("Complete every source decision before preparing the comparison.", true);
    return;
  }
  if (state.reviewDirty && !(await saveDraft(true))) return;
  elements.prepareApprovalButton.disabled = true;
  setReviewMessage("Comparing reviewed cards with the Career Vault...");
  try {
    const preview = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/approval-preview`
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = preview;
    const defaultCount = applyApprovalDefaults(preview);
    setReviewMessage(defaultCount
      ? `${defaultCount} advisory default${defaultCount === 1 ? "" : "s"} prepared. Review or change them, then save decisions.`
      : "Approval preview ready. No Career Vault files were changed."
    );
    renderReview();
    elements.approvalPreview.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    setReviewMessage(formatRequestError(error), true);
    renderReview();
  }
}

async function saveApprovalDecisions() {
  const initialRequest = currentReviewRequest();
  if (!initialRequest || !state.approvalPreview || !state.reviewDirty) return;
  elements.saveApprovalDecisionsButton.disabled = true;
  setReviewMessage("Saving approval decisions to the local review draft...");
  if (!(await saveDraft(true))) {
    if (isCurrentReviewRequest(initialRequest)) renderApprovalPreview();
    return;
  }
  const request = currentReviewRequest();
  if (!request) return;
  try {
    const preview = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/approval-preview`
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = preview;
    setReviewMessage("Approval decisions saved to review.yaml. No Career Vault files were changed.");
    renderReview();
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    setReviewMessage(formatRequestError(error), true);
    renderReview();
  }
}

async function applyApprovalDecisions() {
  const preview = state.approvalPreview;
  const completion = liveCompletionReport();
  const request = currentReviewRequest();
  if (!request || !hasCurrentIntakeApprovalPreview(preview, state.reviewDetail)
      || !completion?.ready_to_approve || state.reviewDirty) {
    setReviewMessage("Save every approval decision before applying it.", true);
    return;
  }
  const canonicalCount = (currentReview()?.approval_decisions || []).filter(
    (decision) => decision.action === "merge" || decision.action === "create"
  ).length;
  const decisions = new Map(
    (currentReview()?.approval_decisions || [])
      .filter((decision) => decision.subject_kind === "entity")
      .map((decision) => [decision.subject_id, decision])
  );
  const pendingNames = new Set();
  (currentReview()?.entities || []).forEach((entity) => {
    const decision = decisions.get(entity.id);
    const applies = decision
      && ["merge", "create"].includes(decision.action)
      && (
        !decision.field_resolutions
        || ["source", "add_source"].includes(decision.field_resolutions.technologies)
      );
    if (applies) {
      (entity.pending_skills || []).forEach((item) => pendingNames.add(normalizeReviewSkillName(item.name)));
    }
  });
  const pendingMessage = pendingNames.size
    ? ` Up to ${pendingNames.size} new custom skill${pendingNames.size === 1 ? "" : "s"} will be created after exact-match validation.`
    : "";
  const confirmed = window.confirm(
    `Apply this completed review to the local Career Vault? ${canonicalCount} card action${canonicalCount === 1 ? "" : "s"} will be written.${pendingMessage} The source review will become read-only.`
  );
  if (!confirmed) return;
  elements.applyApprovalButton.disabled = true;
  setReviewMessage("Validating and applying approval decisions...");
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/apply-approval`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          expected_review_revision: preview.review_revision,
          expected_vault_revision: preview.vault_revision,
          expected_registry_revision: preview.registry_revision,
        }),
      }
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    await loadReview(request.sourceId);
    if (state.reviewSourceId !== request.sourceId || state.activeView !== "review") return;
    const libraryItem = state.extractions.find((item) => item.id === request.sourceId);
    if (libraryItem) {
      Object.assign(libraryItem, {
        workflow_status: "saved_to_vault",
        saved_at: result.applied_at,
        review_entity_count: result.changes.length,
        unresolved_block_count: 0,
        approval_issue_count: 0,
      });
      renderExtractions();
    }
    setReviewMessage(
      `Career Vault updated: ${result.changed_files.length} file${result.changed_files.length === 1 ? "" : "s"} changed${result.created_skill_ids?.length ? `, ${result.created_skill_ids.length} custom skill${result.created_skill_ids.length === 1 ? "" : "s"} created` : ""}. This source review is now read-only.`
    );
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    setReviewMessage(formatRequestError(error), true);
    renderApprovalPreview();
  } finally {
    await pollLogs();
  }
}

function renderApprovedImportResumeAction() {
  const sourceId = currentApprovedReviewSourceId();
  elements.keepApprovedResume.hidden = !sourceId;
  if (!sourceId) return;
  const loading = state.keepApprovedResumeLoadingSourceId === sourceId;
  elements.keepApprovedResumeButton.disabled = loading;
  elements.keepApprovedResumeButton.textContent = loading
    ? "Saving..."
    : "Keep for applications";
}

async function keepApprovedImportForApplications() {
  const sourceId = currentApprovedReviewSourceId();
  if (!sourceId || state.keepApprovedResumeLoadingSourceId) return;
  const request = { sourceId, epoch: reviewLoadEpoch };
  state.keepApprovedResumeLoadingSourceId = sourceId;
  elements.keepApprovedResumeMessage.className = "form-message";
  elements.keepApprovedResumeMessage.textContent = "Saving the approved source as an application Resume Version...";
  renderApprovedImportResumeAction();
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(sourceId)}/keep-for-applications`,
      { method: "POST" }
    );
    if (!isCurrentReviewRequest(request)) return;
    invalidateResumeLibraryManagement();
    await loadResumeLibrary();
    if (!isCurrentReviewRequest(request)) return;
    const resume = result.resume;
    elements.keepApprovedResumeMessage.textContent = result.already_saved
      ? `${resume.display_name} is already available in Applications.`
      : `Saved ${resume.display_name}. It is now available in Applications.`;
    toast.show({
      message: result.already_saved
        ? `Resume already saved: ${resume.display_name}.`
        : `Resume saved: ${resume.display_name}.`,
    });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    elements.keepApprovedResumeMessage.className = "form-message error";
    elements.keepApprovedResumeMessage.textContent = formatRequestError(error);
  } finally {
    if (state.keepApprovedResumeLoadingSourceId === sourceId) {
      state.keepApprovedResumeLoadingSourceId = null;
      renderApprovedImportResumeAction();
    }
  }
}

function renderApprovedImportRemovalAction() {
  const sourceId = currentApprovedReviewSourceId();
  elements.removeApprovedImport.hidden = !sourceId;
  if (!sourceId) return;
  const loading = state.removeApprovedImportLoadingSourceId === sourceId;
  elements.removeApprovedImportButton.disabled = loading;
  elements.removeApprovedImportButton.textContent = loading
    ? "Removing..."
    : "Remove original file";
}

async function removeApprovedImportSource() {
  const sourceId = currentApprovedReviewSourceId();
  if (!sourceId || state.removeApprovedImportLoadingSourceId) return;
  if (!window.confirm(
    "Permanently remove the verified original import file? Career Vault, the review, and its approval audit will remain. You cannot keep this exact file for applications afterwards unless it was saved first."
  )) return;
  const request = { sourceId, epoch: reviewLoadEpoch };
  state.removeApprovedImportLoadingSourceId = sourceId;
  elements.removeApprovedImportMessage.className = "form-message";
  elements.removeApprovedImportMessage.textContent = "Verifying the approved original before removing it...";
  renderApprovedImportRemovalAction();
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(sourceId)}/source`,
      { method: "DELETE" }
    );
    if (!isCurrentReviewRequest(request)) return;
    await loadResumeCollections({ force: true });
    if (!isCurrentReviewRequest(request)) return;
    elements.removeApprovedImportMessage.textContent = result.already_removed
      ? "The original import file was already removed. Career Vault and the approval audit remain available."
      : "Original import file removed. Career Vault, the review, and the approval audit remain available.";
    toast.show({
      message: result.already_removed
        ? "Original import file was already removed."
        : "Original import file removed.",
    });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    elements.removeApprovedImportMessage.className = "form-message error";
    elements.removeApprovedImportMessage.textContent = formatRequestError(error);
  } finally {
    if (state.removeApprovedImportLoadingSourceId === sourceId) {
      state.removeApprovedImportLoadingSourceId = null;
      renderApprovedImportRemovalAction();
    }
  }
}
function closeApprovalPreview() {
  state.approvalPreview = null;
  setReviewMessage("");
  renderReview();
}
function renderReview() {
  const review = currentReview();
  const extraction = state.extractions.find((item) => item.id === state.reviewSourceId);
  elements.reviewPath.textContent = state.reviewDetail?.source_name || extraction?.source_name || "Select an extraction.";
  if (review && !review.entities.some((item) => item.id === state.selectedEntityId)) {
    state.selectedEntityId = review.entities[0]?.id || null;
  }
  document.querySelectorAll("[data-block-filter]").forEach((button) => {
    button.classList.toggle("active", button.dataset.blockFilter === state.blockFilter);
  });
  renderReviewToolbar();
  renderApprovedImportResumeAction();
  renderApprovedImportRemovalAction();
  renderCompletionGate();
  renderApprovalPreview();
  renderEntityHierarchy();
  renderCardEditor();
  renderReviewBlocks();
  if (state.reviewDetail?.approval_application) {
    elements.cardEditor.querySelectorAll("input, textarea, select, button").forEach((control) => {
      control.disabled = true;
    });
    elements.reviewBlockList.querySelectorAll("input, textarea, select, button").forEach((control) => {
      control.disabled = true;
    });
    elements.deleteCardButton.disabled = true;
    elements.restoreCardButton.disabled = true;
    elements.ungroupCardButton.disabled = true;
    elements.acceptCardButton.disabled = true;
  }
}

async function loadReview(sourceId, { epoch = null } = {}) {
  if (!sourceId) return;
  const requestEpoch = epoch ?? beginReviewLoad(sourceId);
  const isCurrent = () => isCurrentReviewLoad(sourceId, requestEpoch);
  renderReview();
  try {
    await loadExtraction(sourceId, { isCurrent });
  } catch (error) {
    if (isCurrent()) setReviewMessage(formatRequestError(error), true);
  }
  if (!isCurrent()) return;
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(sourceId)}`);
    if (!isCurrent() || detail.source_id !== sourceId) return;
    state.reviewDetail = detail;
    if (!state.selectedEntityId) {
      state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    }
    setReviewMessage(state.reviewDetail.approval_application
      ? "This intake review was applied and is now read-only."
      : "");
  } catch (error) {
    if (!isCurrent()) return;
    if (error.status !== 404) {
      setReviewMessage(formatRequestError(error), true);
    } else {
      setReviewMessage("No review exists yet. Build it to begin card work.");
    }
    state.reviewDetail = null;
    state.selectedEntityId = null;
  }
  if (isCurrent()) renderReview();
}

function markReviewDirty() {
  if (state.reviewDetail?.approval_application) return;
  state.reviewDirty = true;
  state.approvalPreview = null;
  renderReviewToolbar();
}

async function buildReview() {
  const request = activeReviewLoadRequest();
  if (!request) return;
  state.approvalPreview = null;
  elements.buildReviewButton.disabled = true;
  setReviewMessage("Building a complete source-block review...");
  try {
    const detail = await requestJson("/api/reviews/build", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        source_id: request.sourceId,
        expected_review_revision: state.reviewDetail?.review_revision || null,
      }),
    });
    if (!isCurrentReviewLoad(request.sourceId, request.epoch) || detail.source_id !== request.sourceId) return;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    const operation = state.reviewDetail.operation;
    const needsInitialDetection = state.reviewDetail.review.entities.length === 0;
    setReviewMessage(`Source inventory ready: ${operation.new_decision_count} new and ${operation.preserved_decision_count} preserved block decisions.`);
    await loadResumeCollections({ force: true });
    if (!isCurrentReviewLoad(request.sourceId, request.epoch)) return;
    if (needsInitialDetection) await suggestCards();
  } catch (error) {
    if (!isCurrentReviewLoad(request.sourceId, request.epoch)) return;
    setReviewMessage(formatRequestError(error), true);
  } finally {
    if (isCurrentReviewLoad(request.sourceId, request.epoch)) renderReview();
    await pollLogs();
  }
}

async function saveDraft(silent = false) {
  const review = currentReview();
  const request = currentReviewRequest();
  if (!review || !request) return false;
  pruneApprovalDecisions(review);
  elements.saveDraftButton.disabled = true;
  if (!silent) setReviewMessage("Saving draft...");
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(request.sourceId)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        review,
        expected_review_revision: state.reviewDetail.review_revision,
      }),
    });
    if (!isCurrentReviewRequest(request) || detail.source_id !== request.sourceId) return false;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    if (!silent) setReviewMessage("Draft saved locally.");
    renderReview();
    return true;
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return false;
    setReviewMessage(formatRequestError(error), true);
    renderReviewToolbar();
    return false;
  } finally {
    await pollLogs();
  }
}

async function suggestCards() {
  if (!currentReview()) return;
  state.approvalPreview = null;
  if (state.reviewDirty && !(await saveDraft(true))) return;
  const request = currentReviewRequest();
  if (!request) return;
  elements.suggestCardsButton.disabled = true;
  setReviewMessage("Detecting cards and linking structurally clear resume sections...");
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(request.sourceId)}/suggest`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expected_review_revision: state.reviewDetail.review_revision,
      }),
    });
    if (!isCurrentReviewRequest(request) || detail.source_id !== request.sourceId) return;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    const stats = state.reviewDetail.suggestion;
    setReviewMessage(
      stats.card_count + " cards found ("
      + stats.profile_card_count + " profile, "
      + stats.experience_card_count + " experience, "
      + stats.education_card_count + " education, "
      + stats.project_card_count + " projects, "
      + stats.summary_card_count + " summary, "
      + stats.skill_card_count + " skill groups, "
      + stats.certification_card_count + " certifications); "
      + stats.structural_block_count + " section markers classified; "
      + stats.new_card_count + " new cards; "
      + stats.auto_assigned_link_count + " structurally clear links added and "
      + stats.suggested_link_count + " links left for review."
    );
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    setReviewMessage(formatRequestError(error), true);
  } finally {
    if (isCurrentReviewRequest(request)) renderReview();
    await pollLogs();
  }
}

function acceptSuggestions() {
  const review = currentReview();
  if (!review) return;
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));
  let accepted = 0;
  review.block_reviews.forEach((item) => {
    const valid = item.suggested_owner_ids.filter((id) => entityById.has(id));
    if (item.disposition === "unassigned" && valid.length === 1) {
      item.disposition = "assigned";
      item.owner_id = valid[0];
      item.category = entityCategory(entityById.get(valid[0]));
      item.suggested_owner_ids = [];
      accepted += 1;
    }
  });
  markReviewDirty();
  setReviewMessage(`${accepted} suggested block links accepted. Save the review to persist them.`);
  renderReview();
}

function acceptSelectedCardSuggestions() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  if (!review || !entity) return;
  let accepted = 0;
  review.block_reviews.forEach((item) => {
    if (
      item.disposition === "unassigned"
      && item.suggested_owner_ids.length === 1
      && item.suggested_owner_ids[0] === entity.id
    ) {
      item.disposition = "assigned";
      item.owner_id = entity.id;
      item.category = entityCategory(entity);
      item.suggested_owner_ids = [];
      accepted += 1;
    }
  });
  markReviewDirty();
  setReviewMessage(
    accepted + " links accepted for " + entityLabel(entity)
    + ". Save draft to persist them."
  );
  renderReview();
}

function moveSelectedCardToReview() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  if (!review || !entity || entity.entity_type === "profile") return;
  entity.review_state = "queued";
  state.blockFilter = "current";
  markReviewDirty();
  setReviewMessage(`${entityLabel(entity)} moved to the review queue with its source blocks intact.`);
  renderReview();
}

function restoreCard(entityId) {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === entityId);
  if (!review || !entity) return;
  entity.review_state = "active";
  state.selectedEntityId = entity.id;
  state.blockFilter = "current";
  markReviewDirty();
  setReviewMessage(`${entityLabel(entity)} restored to the active hierarchy.`);
  renderReview();
}

function ungroupCard(entityId) {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === entityId);
  if (!review || !entity || !entityIsQueued(entity)) return;
  const category = entityCategory(entity);
  if (!window.confirm(`Ungroup ${entityLabel(entity)}? The card will be removed, but its source blocks will stay under ${CATEGORY_LABELS[category] || category} for reassignment.`)) return;
  review.entities = review.entities.filter((item) => item.id !== entity.id);
  review.block_reviews.forEach((item) => {
    if (item.owner_id === entity.id) {
      item.disposition = item.category ? "categorized" : "unassigned";
      item.owner_id = null;
    }
    item.suggested_owner_ids = item.suggested_owner_ids.filter((id) => id !== entity.id);
  });
  state.selectedEntityId = review.entities.find((item) => !entityIsQueued(item))?.id || null;
  state.blockFilter = "all";
  markReviewDirty();
  setReviewMessage(`Card ungrouped. Its source blocks remain under ${CATEGORY_LABELS[category] || category} for reassignment.`);
  renderReview();
}

function groupedRuns() {
  const runs = new Map();
  for (const event of state.logEvents) {
    if (!runs.has(event.run_id)) runs.set(event.run_id, []);
    runs.get(event.run_id).push(event);
  }
  return [...runs.entries()].reverse();
}

function mergeDiagnosticEvents(events) {
  if (!Array.isArray(events) || !events.length) return;
  const eventKeys = new Set(state.logEvents.map((event) => (
    `${event.run_id}:${event.source}:${event.stage}:${event.sequence}`
  )));
  const additions = events.filter((event) => {
    if (!event?.run_id || !event?.source || !event?.stage) return false;
    const key = `${event.run_id}:${event.source}:${event.stage}:${event.sequence}`;
    if (eventKeys.has(key)) return false;
    eventKeys.add(key);
    return true;
  });
  if (!additions.length) return;
  state.logEvents = [...state.logEvents, ...additions]
    .sort((left, right) => String(left.timestamp).localeCompare(String(right.timestamp)))
    .slice(-MAX_LOG_EVENTS);
}

function renderRunSelector() {
  const runs = groupedRuns();
  if (!state.selectedRunId && runs.length) state.selectedRunId = runs[0][0];
  elements.runSelect.innerHTML = runs.length
    ? runs.map(([runId, events]) => {
      const first = events[0];
      return `<option value="${escapeHtml(runId)}">${escapeHtml(first.source)} - ${formatTime(first.timestamp)}</option>`;
    }).join("")
    : '<option value="">No diagnostic runs</option>';
  if (state.selectedRunId) elements.runSelect.value = state.selectedRunId;
}

function renderDiagnostics() {
  renderRunSelector();
  const events = state.logEvents.filter((event) => event.run_id === state.selectedRunId);
  if (!events.length) {
    elements.runSummary.textContent = "No diagnostic run selected.";
    elements.diagnosticEvents.innerHTML = '<tr><td colspan="5" class="empty-state">No events.</td></tr>';
    return;
  }
  const finalEvent = [...events].reverse().find((event) => (
    ["completed", "failed"].includes(event.event)
      && ["run", "review_run", "card_suggestion_run", "review_ui_save", "approval_apply_run", "source_upload", "job_intake_run", "vault_editor_run", "email_connection_run", "email_connection_client", "email_sync_run"].includes(event.stage)
  ));
  const attentionEvent = [...events].reverse().find((event) => (
    ["error", "warning", "success_with_warnings"].includes(event.status)
      && event.details?.error_code
  ));
  const total = finalEvent?.details?.total_duration_ms;
  const durationLabel = Number.isFinite(total)
    ? `${total.toFixed(1)} ms`
    : (finalEvent ? "duration n/a" : "in progress");
  const attention = attentionEvent ? `
    <div class="diagnostic-failure${attentionEvent.status === "error" ? "" : " is-warning"}">
      <strong>${escapeHtml(attentionEvent.details?.error_code || "UNEXPECTED_ERROR")}</strong>
      <span>${escapeHtml(attentionEvent.details?.summary || "The operation needs attention.")}</span>
      ${attentionEvent.details?.error_message ? `<span>Details: ${escapeHtml(attentionEvent.details.error_message)}</span>` : ""}
      ${attentionEvent.details?.suggestion ? `<span>Next: ${escapeHtml(attentionEvent.details.suggestion)}</span>` : ""}
      <span>Run: ${escapeHtml(attentionEvent.run_id)}</span>
    </div>` : "";
  elements.runSummary.innerHTML = `
    <div><strong>${escapeHtml(events[0].source)}</strong>
    <span> / ${events.length} events / ${durationLabel}</span>
    ${finalEvent ? ` / <span class="${statusClass(finalEvent.status)}">${escapeHtml(finalEvent.status)}</span>` : ""}</div>
    ${attention}`;
  elements.diagnosticEvents.innerHTML = events.map((event) => {
    const duration = event.details?.duration_ms;
    const details = { ...event.details };
    delete details.duration_ms;
    return `
      <tr>
        <td>${escapeHtml(event.stage)}</td>
        <td>${escapeHtml(event.event)}</td>
        <td class="status-text ${statusClass(event.status)}">${escapeHtml(event.status)}</td>
        <td>${Number.isFinite(duration) ? `${duration.toFixed(1)} ms` : "-"}</td>
        <td><pre class="details-code">${escapeHtml(JSON.stringify(details, null, 2))}</pre></td>
      </tr>`;
  }).join("");
}

function updateLogBar(event) {
  if (!event) return;
  const duration = event.details?.duration_ms;
  const visualStatus = event.status === "success_with_warnings" ? "warning" : event.status;
  elements.statusDot.className = `status-dot ${visualStatus}`;
  elements.logStage.textContent = `${event.stage} / ${event.event}`;
  elements.logStatus.textContent = event.status;
  elements.logDuration.textContent = Number.isFinite(duration) ? `${duration.toFixed(1)} ms` : "";
  elements.logRun.textContent = event.run_id;
}

let diagnosticsResetOnRetry = false;

function showDiagnosticsHistory(message = "", retryLabel = "") {
  elements.diagnosticsHistory.hidden = !message;
  elements.diagnosticsHistoryStatus.textContent = message;
  elements.diagnosticsHistoryRetry.hidden = !retryLabel;
  elements.diagnosticsHistoryRetry.textContent = retryLabel;
}

async function pollLogs() {
  if (state.activeView !== "diagnostics" || diagnosticsPollInFlight || diagnosticsPollPaused
    || Date.now() < diagnosticsRetryAfter) return;
  diagnosticsPollInFlight = true;
  let changed = false;
  try {
    let hasMore = false;
    for (let pageNumber = 0; pageNumber < DIAGNOSTIC_PAGES_PER_POLL; pageNumber += 1) {
      const payload = await requestJson(`/api/logs?after=${encodeURIComponent(state.logCursor)}`);
      if (state.activeView !== "diagnostics") return;
      const page = parseDiagnosticPage(payload, state.logCursor);
      state.logCursor = page.cursor;
      hasMore = page.hasMore;
      if (page.events.length) {
        mergeDiagnosticEvents(page.events);
        if (!state.logSelectionPinned) {
          state.selectedRunId = state.logEvents.at(-1)?.run_id || null;
        }
        updateLogBar(page.events.at(-1));
        changed = true;
      }
      if (!hasMore) break;
    }
    if (!hasMore && state.logSelectionPinned
      && !state.logEvents.some((event) => event.run_id === state.selectedRunId)) {
      state.logSelectionPinned = false;
      state.selectedRunId = state.logEvents.at(-1)?.run_id || null;
      changed = true;
    }
    diagnosticsRetryAfter = 0;
    diagnosticsResetOnRetry = false;
    showDiagnosticsHistory(hasMore ? "Reading saved diagnostic history…" : "");
  } catch (error) {
    if (state.activeView !== "diagnostics") return;
    const failure = classifyDiagnosticReadFailure(error);
    diagnosticsPollPaused = failure.paused;
    diagnosticsResetOnRetry = failure.resetCursor;
    diagnosticsRetryAfter = failure.paused ? 0 : Date.now() + 3000;
    elements.statusDot.className = "status-dot error";
    elements.logStage.textContent = "diagnostics read failed";
    elements.logStatus.textContent = failure.code || "read failed";
    showDiagnosticsHistory(
      formatRequestError(error),
      failure.paused ? (failure.resetCursor ? "Read from beginning" : "Retry reading") : "",
    );
  } finally {
    if (changed && state.activeView === "diagnostics") renderDiagnostics();
    diagnosticsPollInFlight = false;
  }
}

function startDiagnosticsPolling() {
  if (diagnosticsPollTimer !== null) return;
  if (!diagnosticsPollPaused && state.logCursor === 0 && !state.logEvents.length) {
    showDiagnosticsHistory("Reading saved diagnostic history…");
  }
  void pollLogs();
  diagnosticsPollTimer = window.setInterval(() => {
    void pollLogs();
  }, 750);
}

function stopDiagnosticsPolling() {
  if (diagnosticsPollTimer === null) return;
  window.clearInterval(diagnosticsPollTimer);
  diagnosticsPollTimer = null;
}

elements.diagnosticsHistoryRetry.addEventListener("click", () => {
  if (diagnosticsResetOnRetry) {
    state.logCursor = 0;
    state.logEvents = [];
    state.selectedRunId = null;
    state.logSelectionPinned = false;
    renderDiagnostics();
  }
  diagnosticsPollPaused = false;
  diagnosticsRetryAfter = 0;
  diagnosticsResetOnRetry = false;
  showDiagnosticsHistory("Reading saved diagnostic history…");
  void pollLogs();
});

async function loadJobSourceFile() {
  const file = elements.jobSourceFile.files[0];
  if (!file) return;
  const suffix = file.name.toLowerCase().split(".").pop();
  if (!["txt", "md"].includes(suffix)) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Choose a TXT or Markdown file.";
    elements.jobSourceFile.value = "";
    return;
  }
  if (file.size > 500_000) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Job description must not exceed 500,000 bytes.";
    elements.jobSourceFile.value = "";
    return;
  }
  try {
    elements.jobSourceText.value = await file.text();
    elements.jobSourceName.value = file.name;
    resetJobIntakeIdempotency();
    elements.jobMessage.className = "form-message";
    elements.jobMessage.textContent = `Loaded ${file.name}`;
    updateJobCharacterCount();
  } catch (error) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = `Could not read ${file.name}: ${error.message}`;
  }
}

async function reanalyzeSelectedJob({ automatic = false } = {}) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job || job.status !== "draft") return;
  const requestedJobId = job.id;
  const message = elements.jobLibraryMessage;
  const originalLabel = elements.reanalyzeJobButton.textContent;
  elements.reanalyzeJobButton.disabled = true;
  elements.reanalyzeJobButton.textContent = "Refreshing...";
  if (automatic) {
    message.className = "form-message job-library-application-message";
    message.textContent = `Updating ${job.company || job.title || job.id} with the current analyzer...`;
  }
  try {
    const result = await api.reanalyzeJob(job.id, job.card_revision);
    if (state.selectedJobId !== requestedJobId) return;
    state.selectedJobId = result.job.id;
    await loadJobs({ force: true, autoReanalyze: false });
    message.className = "form-message job-library-application-message";
    message.textContent = "Analysis refreshed.";
  } catch (error) {
    const stateAfterFailure = error?.payload?.operation_state;
    if (api.capabilities.job_reanalysis_revision === true && (
      stateAfterFailure?.revision_refresh_required === true ||
      stateAfterFailure?.committed === true || stateAfterFailure?.known === false
    )) {
      try {
        const latest = (await api.getJob(requestedJobId))?.job;
        if (latest?.id === requestedJobId) {
          upsertJobInState(latest);
          renderJobs();
        }
      } catch { /* Preserve the original operation error; no blind retry. */ }
    }
    message.className = "form-message job-library-application-message error";
    message.textContent = formatRequestError(error);
  } finally {
    const refreshedJob = state.jobs.find((item) => item.id === state.selectedJobId);
    elements.reanalyzeJobButton.disabled = refreshedJob?.status !== "draft";
    elements.reanalyzeJobButton.textContent = refreshedJob?.status === "draft" ? originalLabel : "Analysis locked";
    await pollLogs();
  }
}

async function loadSelectedJobSource() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job) return;
  const originalLabel = elements.loadJobSourceButton.textContent;
  elements.loadJobSourceButton.disabled = true;
  elements.loadJobSourceButton.textContent = "Loading...";
  setJobWorkspaceTab("details");
  try {
    const source = await requestJson(`/api/jobs/${encodeURIComponent(job.id)}/source`);
    state.jobSourcePreview = {
      jobId: job.id,
      sourceName: source.source_name || job.raw_jd_path || "Saved source",
      sourceText: source.source_text || "",
    };
    renderJobDigest();
    elements.jobSourcePreview.open = true;
    elements.jobSourcePreview.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    elements.jobSelectionMeta.textContent = formatRequestError(error);
  } finally {
    elements.loadJobSourceButton.disabled = false;
    elements.loadJobSourceButton.textContent = originalLabel;
  }
}
function upsertJobInState(job) {
  const existingIndex = state.jobs.findIndex((item) => item.id === job.id);
  if (existingIndex >= 0) {
    state.jobs.splice(existingIndex, 1, job);
  } else {
    state.jobs.unshift(job);
  }
}

function resetJobIntakeIdempotency() {
  state.jobIntakeIdempotencyKey = "";
}

function jobIntakeIdempotencyKey() {
  if (!state.jobIntakeIdempotencyKey) {
    state.jobIntakeIdempotencyKey = globalThis.crypto?.randomUUID
      ? globalThis.crypto.randomUUID()
      : `intake-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }
  return state.jobIntakeIdempotencyKey;
}

async function createJobDraft(initialStatus = "captured") {
  const sourceText = elements.jobSourceText.value;
  if (!sourceText.trim()) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Paste or load a complete job description first.";
    return;
  }
  const isApplied = initialStatus === "applied";
  const idempotencyKey = jobIntakeIdempotencyKey();
  elements.createJobButton.disabled = true;
  elements.createAppliedJobButton.disabled = true;
  elements.createJobButton.textContent = isApplied ? "Saving..." : "Creating...";
  elements.createAppliedJobButton.textContent = isApplied ? "Saving..." : "Save as applied";
  elements.jobMessage.className = "form-message";
  elements.jobMessage.textContent = isApplied
    ? "Saving the posting, assessment, and Applied application..."
    : "Saving and analyzing the posting...";
  try {
    const result = await api.createJob({
      source_text: sourceText,
      source_name: elements.jobSourceName.value,
      company: elements.jobCompany.value,
      title: elements.jobTitle.value,
      source_url: elements.jobSourceUrl.value,
      posting_id: elements.jobPostingId.value,
      idempotency_key: idempotencyKey,
      initial_status: initialStatus,
    });
    state.selectedJobId = result.job.id;
    elements.jobMessage.textContent = result.duplicate
      ? isApplied
        ? "This posting was already saved. The existing application is now marked Applied."
        : "This posting was already saved. Opening the existing job."
      : isApplied
        ? "Applied application saved."
        : "Job saved and analyzed.";
    elements.jobSourceFile.value = "";
    elements.jobSourceText.value = "";
    elements.jobSourceName.value = "";
    elements.jobCompany.value = "";
    elements.jobTitle.value = "";
    elements.jobSourceUrl.value = "";
    elements.jobPostingId.value = "";
    resetJobIntakeIdempotency();
    updateJobCharacterCount();
    upsertJobInState(result.job);
    state.jobAssessment = result.job.assessment || null;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
    switchView("job-library");
    renderJobs();
    if (!state.jobAssessment) await loadSelectedJobAssessment();
    if (result.duplicate) {
      elements.jobLibraryMessage.className = "form-message";
      elements.jobLibraryMessage.textContent = isApplied
        ? "This exact job description was already captured. The existing application is now marked Applied."
        : "This exact job description was already captured. The existing record is open.";
    }
  } catch (error) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = formatRequestError(error);
  } finally {
    elements.createJobButton.disabled = false;
    elements.createJobButton.textContent = "Save and analyze";
    elements.createAppliedJobButton.disabled = false;
    elements.createAppliedJobButton.textContent = "Save as applied";
    await pollLogs();
  }
}

async function runExtraction() {
  await runExtractionForSource(elements.sourceSelect.value);
}

async function prepareReviewForSource(sourceId) {
  let existing = null;
  try {
    existing = await requestJson(`/api/reviews/${encodeURIComponent(sourceId)}`);
  } catch (error) {
    if (error.status !== 404) throw error;
  }
  const built = await requestJson("/api/reviews/build", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      source_id: sourceId,
      ...(existing?.review_revision
        ? { expected_review_revision: existing.review_revision }
        : {}),
    }),
  });
  if (built.review.entities.length) return built;
  return requestJson(`/api/reviews/${encodeURIComponent(sourceId)}/suggest`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ expected_review_revision: built.review_revision }),
  });
}

async function runExtractionForSource(sourceName) {
  const source = sourceName;
  if (!source) return;
  elements.runButton.disabled = true;
  elements.runButton.textContent = "Running...";
  elements.runMessage.className = "form-message";
  elements.runMessage.textContent = `Extracting ${source}`;
  try {
    const result = await requestJson("/api/extract", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source, overwrite: elements.overwriteInput.checked }),
    });
    elements.runMessage.textContent = "Building the review and linking clear resume sections...";
    await prepareReviewForSource(result.source_id);
    elements.runMessage.textContent = `Ready to review: ${result.technology_count} technologies, ${result.bullet_count} bullets.`;
    await loadResumeCollections({ force: true });
    await openResumeReview(result.source_id);
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  } finally {
    elements.runButton.disabled = false;
    elements.runButton.textContent = "Extract and review";
    await pollLogs();
  }
}

async function continueResumeReview(sourceId) {
  if (!sourceId) return;
  await openResumeReview(sourceId);
}

async function uploadDocument({ processAgain = false } = {}) {
  const file = elements.sourceUpload.files[0];
  if (!file) {
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = "Choose a TXT or DOCX document first.";
    return;
  }
  const check = state.sourceUploadCheck?.file === file ? state.sourceUploadCheck : null;
  if (state.sourceUploadCheckLoading || (!processAgain && (check?.error || check?.prior_imports?.length || check?.same_name_conflict))) {
    renderExactReimportPanel();
    return;
  }
  elements.uploadButton.disabled = true;
  elements.uploadButton.textContent = "Extracting...";
  elements.uploadMessage.className = "form-message upload-message";
  elements.uploadMessage.textContent = processAgain
    ? `Creating a separate review for ${file.name}`
    : `Adding and analyzing ${file.name}`;
  try {
    const result = await requestJson("/api/upload", {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Source-Name": encodeURIComponent(file.name),
        ...(processAgain ? { "X-Source-Reimport": "process-again" } : {}),
      },
      body: file,
    });
    await loadResumeCollections({ force: true });
    elements.sourceSelect.value = result.name;
    elements.uploadMessage.textContent = result.reimported
      ? `Added locally as ${result.name}. Creating a new review...`
      : `Added locally: ${result.name}`;
    await runExtractionForSource(result.name);
  } catch (error) {
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = formatRequestError(error);
  } finally {
    if (elements.sourceUpload.files[0] === file) {
      elements.sourceUpload.value = "";
    }
    clearSourceUploadCheck();
    elements.uploadButton.disabled = false;
    elements.uploadButton.textContent = "Extract and review";
    await pollLogs();
  }
}

elements.primaryNavigation.addEventListener("click", (event) => {
  const button = event.target.closest(".nav-item[data-view]");
  if (!button || !elements.primaryNavigation.contains(button)) return;
  button.closest("details")?.removeAttribute("open");
  setMobileNavigation(false);
  void navigateToView(button.dataset.view);
});
initHelpCenter({ onNavigate: navigateFromHelp });
elements.mobileNavigationToggle.addEventListener("click", () => {
  setMobileNavigation(!document.body.classList.contains("navigation-open"));
});
elements.mobileNavigationClose.addEventListener("click", () => {
  setMobileNavigation(false);
  elements.mobileNavigationToggle.focus();
});
elements.sidebarBackdrop.addEventListener("click", () => {
  setMobileNavigation(false);
  elements.mobileNavigationToggle.focus();
});
mobileNavigationMedia.addEventListener("change", () => setMobileNavigation(false));
elements.mobileAddButton.addEventListener("click", openMobileQuickActions);
document.querySelector("#open-diagnostics").addEventListener("click", () => { void navigateToView("diagnostics"); });
document.querySelector("#refresh-active-view").addEventListener("click", refreshActiveView);
const interfaceSettingsPanel = document.querySelector("#interface-settings-panel");
const interfaceSettingsTrigger = document.querySelector("#open-interface-settings");
interfaceSettingsPanel.addEventListener("toggle", () => {
  interfaceSettingsTrigger.setAttribute(
    "aria-expanded",
    String(interfaceSettingsPanel.matches(":popover-open")),
  );
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (document.body.classList.contains("navigation-open")) {
    event.preventDefault();
    setMobileNavigation(false);
    elements.mobileNavigationToggle.focus();
    return;
  }
  if (interfaceSettingsPanel.matches(":popover-open")) {
    event.preventDefault();
    interfaceSettingsPanel.hidePopover();
    interfaceSettingsTrigger.focus();
  }
}, { capture: true });
document.querySelectorAll('[data-view="documents"]:not(.nav-item)').forEach((button) => button.addEventListener("click", () => { void navigateToView("documents"); }));
elements.skillsHubModeButtons.forEach((button, index) => {
  button.addEventListener("click", () => setSkillsHubMode(button.dataset.skillsHubMode));
  button.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
    event.preventDefault();
    const offset = event.key === "ArrowRight" ? 1 : -1;
    const next = elements.skillsHubModeButtons[(index + offset + elements.skillsHubModeButtons.length) % elements.skillsHubModeButtons.length];
    setSkillsHubMode(next.dataset.skillsHubMode);
    next.focus();
  });
});
function applyCompetencyFilters() {
  state.selectedCompetencyId = null;
  renderCompetencyExplorer();
}

elements.competencySearch.addEventListener("input", applyCompetencyFilters);
elements.competencyGroupFilter.addEventListener("change", applyCompetencyFilters);
elements.competencyOriginFilter.addEventListener("change", applyCompetencyFilters);
elements.competencyEvidenceFilter.addEventListener("change", applyCompetencyFilters);
elements.competencyDomainFilter.addEventListener("change", applyCompetencyFilters);
elements.competencyKindFilter.addEventListener("change", applyCompetencyFilters);
elements.competencyList.addEventListener("click", (event) => {
  const row = event.target.closest("[data-competency-id]");
  if (row) selectCompetency(row.dataset.competencyId);
});
elements.competencyDetail.addEventListener("click", (event) => {
  const manageSkill = event.target.closest("[data-manage-registry-skill]");
  if (manageSkill) {
    manageRegistrySkill(manageSkill.dataset.manageRegistrySkill);
    return;
  }
  const record = event.target.closest("[data-competency-record-id]");
  if (record) {
    void openCompetencyRecord(
      record.dataset.competencyRecordView,
      record.dataset.competencyRecordId,
    );
    return;
  }
  if (event.target.closest("[data-competency-list-return]")) {
    elements.competencySearch.focus();
    elements.competencyList.scrollIntoView({ behavior: "smooth", block: "start" });
  }
});
elements.demandGapsPeriodForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void applyDemandGapsPeriod();
});
elements.demandGapsPeriodPreset.addEventListener("change", () => {
  void setDemandGapsPeriodPreset(elements.demandGapsPeriodPreset.value);
});
elements.demandGapsDomain.addEventListener("change", renderDemandGaps);
elements.demandGapsRequirement.addEventListener("change", renderDemandGaps);
elements.demandGapsSort.addEventListener("change", renderDemandGaps);
elements.demandGapsSearch.addEventListener("input", renderDemandGaps);
elements.demandGapsWorkflow.addEventListener("click", (event) => {
  const showAll = event.target.closest("[data-demand-gaps-show-all]");
  if (showAll) {
    void setDemandGapsPeriodPreset("all");
    return;
  }
  const coverageButton = event.target.closest("[data-demand-gap-coverage]");
  if (coverageButton) {
    setDemandGapCoverageFilter(coverageButton.dataset.demandGapCoverage);
    return;
  }
  const listAction = event.target.closest("[data-demand-gaps-list-action]");
  if (listAction) {
    const value = listAction.dataset.demandGapsListAction === "all"
      ? ""
      : listAction.dataset.demandGapsListAction;
    setDemandGapCoverageFilter(value, { scroll: true });
    return;
  }
  const skill = event.target.closest("[data-demand-gap-id]");
  if (skill) openDemandGapDrawer(skill.dataset.demandGapId);
});
elements.demandGapDrawerBackdrop.addEventListener("click", closeDemandGapDrawer);
elements.closeDemandGapDrawer.addEventListener("click", closeDemandGapDrawer);
elements.openDemandGapSkill.addEventListener("click", () => {
  if (state.selectedDemandGapId) void openDemandGapSkill(state.selectedDemandGapId);
});
elements.toggleDemandGapJobs.addEventListener("click", () => {
  state.demandGapsRelatedJobsExpanded = !state.demandGapsRelatedJobsExpanded;
  renderDemandGapDrawer();
});
elements.demandGapRelatedJobs.addEventListener("click", (event) => {
  const job = event.target.closest("[data-demand-gap-job-id]");
  if (job) void openDemandGapJob(job.dataset.demandGapJobId);
});
elements.resumeLibrarySearch.addEventListener("input", renderResumeLibrary);
elements.resumeLibraryDigestFilter.addEventListener("change", () => {
  state.resumeLibraryDigestFilter = elements.resumeLibraryDigestFilter.value;
  renderResumeLibrary();
});
elements.resumeLibrarySort.addEventListener("change", () => {
  state.resumeLibrarySort = elements.resumeLibrarySort.value;
  renderResumeLibrary();
});
elements.resumeLibraryArchivedFilter.addEventListener("click", () => setResumeLibraryVisibility(
  state.resumeLibraryVisibility === "archived" ? "active" : "archived"
));
elements.resumeLibraryAddResumeButton.addEventListener("click", openApplicationResumeIntake);
elements.resumeLibraryRecordList.addEventListener("click", async (event) => {
  const actionButton = event.target.closest("[data-resume-library-action]");
  if (actionButton) {
    const action = actionButton.dataset.resumeLibraryAction;
    if (action === "download") {
      await downloadResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
    } else if (action === "delete") {
      await deleteResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
    } else if (action === "digest") {
      await digestResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
    } else if (action === "continue-digest") {
      await openResumeReview(actionButton.dataset.resumeDigestSourceId);
    } else {
      await setResumeLibraryArchiveState(
        actionButton.dataset.resumeLibraryId,
        action === "archive"
      );
    }
    return;
  }
  if (event.target.closest("[data-add-application-resume]")) openApplicationResumeIntake();
});
elements.cancelProfileButton.addEventListener("click", cancelProfileEdit);
elements.editProfileButton.addEventListener("click", () => {
  setProfileEditing(true);
  elements.profileName.focus();
});
elements.profileForm.addEventListener("submit", saveVaultProfile);
elements.profileForm.addEventListener("input", (event) => {
  if (!event.target.closest(".profile-policy-card, .eligibility-profile-section")) {
    state.profileFormDirty = true;
  }
});
elements.profileForm.addEventListener("change", (event) => {
  if (!event.target.closest(".profile-policy-card, .eligibility-profile-section")) {
    state.profileFormDirty = true;
  }
});
elements.profileWorkAuthorization.addEventListener("change", syncProfileWorkAuthorizationOtherField);
[
  elements.eligibilityCurrentStatus,
  elements.eligibilityWorkAuthorization,
  elements.eligibilitySponsorshipNow,
  elements.eligibilitySponsorshipFuture,
  elements.eligibilityCitizenshipConflict,
  elements.eligibilityClearanceConflict,
  elements.eligibilityStemOpt,
].forEach((input) => input.addEventListener("change", markEligibilityProfileDirty));
elements.eligibilityCurrentStatus.addEventListener("input", markEligibilityProfileDirty);
elements.eligibilityWorkAuthorization.addEventListener("input", markEligibilityProfileDirty);
elements.saveEligibilityProfileButton.addEventListener("click", saveEligibilityProfile);
[
  elements.hardBlockerWorkAuthorization,
  elements.hardBlockerClearance,
  elements.hardBlockerExperience,
].forEach((input) => input.addEventListener("change", markHardBlockerPolicyDirty));
elements.saveHardBlockerPolicyButton.addEventListener("click", saveHardBlockerPolicy);
elements.careerVaultLayoutButtons.forEach((button) => {
  button.addEventListener("click", () => setCareerVaultLayout(button.dataset.vaultLayout));
});
document.querySelectorAll("[data-close-vault-card-detail]").forEach((button) => {
  button.addEventListener("click", () => closeCareerVaultCardDetail(button.dataset.closeVaultCardDetail));
});
elements.newExperienceButton.addEventListener("click", beginNewExperience);
elements.vaultArchivedFilter.addEventListener("click", () => setVaultVisibility(
  state.vaultVisibility === "archived" ? "active" : "archived"
));
elements.archiveVaultButton.addEventListener("click", toggleVaultExperienceArchive);
elements.cancelVaultButton.addEventListener("click", cancelVaultEdit);
elements.vaultExperienceForm.addEventListener("submit", saveVaultExperience);
elements.vaultExperienceForm.addEventListener("input", () => { state.vaultFormDirty = true; });
elements.addVaultProofPoint.addEventListener("click", addVaultProofPoint);
elements.vaultProofPointList.addEventListener("input", (event) => {
  if (!event.target.matches("[data-vault-proof-value]")) return;
  updateVaultProofPoint(Number(event.target.dataset.vaultProofValue), event.target.value);
});
elements.vaultProofPointList.addEventListener("click", (event) => {
  const remove = event.target.closest("[data-vault-proof-remove]");
  if (remove) {
    removeVaultProofPoint(Number(remove.dataset.vaultProofRemove));
    return;
  }
  const move = event.target.closest("[data-vault-proof-move]");
  if (move) moveVaultProofPoint(Number(move.dataset.vaultProofIndex), move.dataset.vaultProofMove);
});
elements.vaultSearch.addEventListener("input", () => {
  closeCareerVaultCardDetail("experience");
  renderVaultExperienceList();
});
elements.vaultCategoryFilter.addEventListener("change", () => {
  closeCareerVaultCardDetail("experience");
  renderVaultExperienceList();
});
elements.vaultExperienceList.addEventListener("click", (event) => {
  const record = event.target.closest("[data-vault-experience-id]");
  if (record) selectVaultExperience(record.dataset.vaultExperienceId);
});
function bindManualSkillSelector(kind, addButton) {
  const nodes = manualSkillNodes(kind);
  addButton.addEventListener("click", () => addManualSkills(kind));
  nodes.input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === ",") { event.preventDefault(); addManualSkills(kind); }
  });
  const root = nodes.pills.closest("[data-vault-skill-selector]");
  root.addEventListener("click", (event) => {
    const removeId = event.target.closest("[data-manual-skill-remove]");
    const removePending = event.target.closest("[data-manual-pending-remove]");
    if (removeId) state.manualSkills[kind].ids = state.manualSkills[kind].ids.filter((id) => id !== removeId.dataset.manualSkillRemove);
    if (removePending) state.manualSkills[kind].pending.splice(Number(removePending.dataset.manualPendingRemove), 1);
    if (removeId || removePending) { if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true; renderManualSkillSelector(kind); }
  });
  root.addEventListener("change", (event) => {
    const select = event.target.closest("[data-manual-pending-group]");
    if (!select) return;
    state.manualSkills[kind].pending[Number(select.dataset.manualPendingGroup)].primary_group_id = select.value;
    if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true;
  });
}
bindManualSkillSelector("experience", elements.addVaultSkills);
bindManualSkillSelector("project", elements.addProjectSkills);
elements.newProjectButton.addEventListener("click", beginNewProject);
elements.projectArchivedFilter.addEventListener("click", () => setProjectVisibility(
  state.projectVisibility === "archived" ? "active" : "archived"
));
elements.archiveProjectButton.addEventListener("click", toggleVaultProjectArchive);
elements.cancelProjectButton.addEventListener("click", cancelProjectEdit);
elements.projectForm.addEventListener("submit", saveVaultProject);
elements.projectForm.addEventListener("input", () => { state.projectFormDirty = true; });
elements.projectForm.addEventListener("change", () => { state.projectFormDirty = true; });
elements.projectSearch.addEventListener("input", () => {
  closeCareerVaultCardDetail("projects");
  renderProjectList();
});
elements.projectCategoryFilter.addEventListener("change", () => {
  closeCareerVaultCardDetail("projects");
  renderProjectList();
});
elements.projectRecordList.addEventListener("click", (event) => {
  const record = event.target.closest("[data-project-id]");
  if (record) selectVaultProject(record.dataset.projectId);
});
document.querySelectorAll("[data-open-skill-quick-action]").forEach((button) => {
  button.addEventListener("click", () => {
    void openSkillQuickAction(button.dataset.openSkillQuickAction);
  });
});
document.querySelectorAll("[data-skill-quick-mode]").forEach((button) => {
  button.addEventListener("click", () => setSkillQuickMode(button.dataset.skillQuickMode));
});
elements.skillQuickName.addEventListener("input", () => {
  if (state.skillQuickMode === "skill" && /[,;\n]/.test(elements.skillQuickName.value)) {
    addSkillQuickNames(elements.skillQuickName.value);
  }
});
elements.skillQuickName.addEventListener("keydown", (event) => {
  if (state.skillQuickMode !== "skill" || event.key !== "Enter" || !elements.skillQuickName.value.trim()) return;
  event.preventDefault();
  addSkillQuickNames(elements.skillQuickName.value);
});
elements.skillQuickPills.addEventListener("click", (event) => {
  const remove = event.target.closest("[data-remove-skill-quick-name]");
  if (!remove) return;
  state.skillQuickNames = state.skillQuickNames.filter(
    (_, index) => index !== Number(remove.dataset.removeSkillQuickName),
  );
  renderSkillQuickNames();
  elements.skillQuickName.focus();
});
elements.skillQuickForm.addEventListener("submit", saveSkillQuickAction);
elements.closeSkillQuickDialog.addEventListener("click", closeSkillQuickAction);
elements.cancelSkillQuickDialog.addEventListener("click", closeSkillQuickAction);
elements.skillQuickDialog.addEventListener("click", (event) => {
  if (event.target === elements.skillQuickDialog) closeSkillQuickAction();
});
elements.skillsRegistryLayoutButtons.forEach((button) => {
  button.addEventListener("click", () => {
    setSkillsRegistryLayout(button.dataset.registryLayout);
    renderSkillsRegistry();
  });
});
elements.skillsRegistrySort.addEventListener("change", () => {
  state.registryGroupSort = elements.skillsRegistrySort.value;
  renderSkillsRegistry();
});
elements.skillsRegistryArchivedFilter.addEventListener("click", () => {
  state.registryVisibility = state.registryVisibility === "archived" ? "active" : "archived";
  state.registrySkillVisibility = state.registryVisibility;
  state.selectedRegistrySkillId = null;
  state.selectedRegistryGroupId = null;
  state.registryCardDetailOpen = false;
  renderSkillsRegistry();
});
elements.skillsRegistrySearch.addEventListener("input", () => {
  state.selectedRegistrySkillId = null;
  state.registryGroupEditing = false;
  state.registryCardDetailOpen = false;
  renderSkillsRegistry();
});
elements.skillsRegistryGrid.addEventListener("click", (event) => {
  const skillButton = event.target.closest("[data-registry-skill-id]");
  if (skillButton) {
    const skill = registrySkill(skillButton.dataset.registrySkillId);
    state.selectedRegistrySkillId = skill?.id || null;
    state.selectedRegistryGroupId = skill?.primary_group_id || state.selectedRegistryGroupId;
    state.registryGroupEditing = false;
    renderSkillsRegistry();
    loadHistoricalEvidenceIfNeeded(skill);
    return;
  }
  const groupButton = event.target.closest("[data-registry-group-id]");
  if (groupButton) {
    state.selectedRegistryGroupId = groupButton.dataset.registryGroupId;
    state.registrySkillVisibility = registryGroup(groupButton.dataset.registryGroupId)?.archived
      ? "archived"
      : "active";
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
    state.registryCardDetailOpen = state.skillsRegistryLayout === "cards";
    renderSkillsRegistry();
  }
});
elements.skillsRegistryDetail.addEventListener("submit", saveSkillsRegistryDetail);
elements.skillsRegistryDetail.addEventListener("click", (event) => {
  const openEvidence = event.target.closest("[data-open-skill-evidence]");
  if (openEvidence) {
    openSkillEvidence(openEvidence.dataset.openSkillEvidence);
    return;
  }
  const evidenceRecord = event.target.closest("[data-competency-record-id]");
  if (evidenceRecord) {
    void openCompetencyRecord(
      evidenceRecord.dataset.competencyRecordView,
      evidenceRecord.dataset.competencyRecordId,
    );
    return;
  }
  const archiveFilter = event.target.closest("[data-registry-skills-archive-filter]");
  if (archiveFilter) {
    state.registrySkillVisibility = state.registrySkillVisibility === "archived" ? "active" : "archived";
    state.selectedRegistrySkillId = null;
    renderSkillsRegistryDetail();
    return;
  }
  const closeCardDetailButton = event.target.closest("[data-close-registry-card-detail]");
  if (closeCardDetailButton) {
    state.registryCardDetailOpen = false;
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
    renderSkillsRegistry();
    return;
  }
  const skillButton = event.target.closest("[data-registry-skill-id]");
  if (skillButton) {
    const skill = registrySkill(skillButton.dataset.registrySkillId);
    state.selectedRegistrySkillId = skill?.id || null;
    state.selectedRegistryGroupId = skill?.primary_group_id || state.selectedRegistryGroupId;
    state.registryGroupEditing = false;
    renderSkillsRegistry();
    loadHistoricalEvidenceIfNeeded(skill);
    return;
  }
  const backButton = event.target.closest("[data-registry-back-group]");
  if (backButton) {
    state.selectedRegistryGroupId = backButton.dataset.registryBackGroup;
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
    renderSkillsRegistry();
    return;
  }
  const editGroupButton = event.target.closest("[data-edit-registry-group]");
  if (editGroupButton) {
    state.registryGroupEditing = true;
    renderSkillsRegistryDetail();
    elements.skillsRegistryDetail.querySelector('input[name="name"]')?.focus();
    return;
  }
  const cancelGroupEditButton = event.target.closest("[data-cancel-registry-group-edit]");
  if (cancelGroupEditButton) {
    state.registryGroupEditing = false;
    renderSkillsRegistryDetail();
    return;
  }
  const addButton = event.target.closest("[data-add-skill-to-group]");
  if (addButton) {
    void openSkillQuickAction("skill", {
      preferredGroupId: addButton.dataset.addSkillToGroup,
    });
    return;
  }
  const archiveButton = event.target.closest("[data-registry-archive-kind]");
  if (archiveButton) {
    void toggleSkillsRegistryArchive(
      archiveButton.dataset.registryArchiveKind,
      archiveButton.dataset.registryArchiveId,
      archiveButton.dataset.registryArchived === "true",
    );
  }
});
elements.skillsRegistryDetail.addEventListener("change", (event) => {
  const sort = event.target.closest("[data-registry-skill-sort]");
  if (!sort) return;
  state.registrySkillSort = sort.value;
  renderSkillsRegistryDetail();
});
elements.newEducationButton.addEventListener("click", beginNewEducation);
elements.educationSchoolFilter.addEventListener("click", () => setEducationType("school"));
elements.educationCertificationFilter.addEventListener("click", () => setEducationType("certification"));
elements.educationArchivedFilter.addEventListener("click", () => setEducationVisibility(
  state.educationVisibility === "archived" ? "active" : "archived"
));
elements.archiveEducationButton.addEventListener("click", toggleEducationArchive);
elements.cancelEducationButton.addEventListener("click", cancelEducationEdit);
elements.educationForm.addEventListener("submit", saveVaultEducation);
elements.educationForm.addEventListener("input", () => { state.educationFormDirty = true; });
elements.educationSearch.addEventListener("input", () => {
  closeCareerVaultCardDetail("education");
  renderEducationList();
});
elements.educationRecordList.addEventListener("click", (event) => {
  const record = event.target.closest("[data-education-id]");
  if (record) selectEducationRecord(record.dataset.educationId);
});
elements.runButton.addEventListener("click", runExtraction);
elements.uploadButton.addEventListener("click", uploadDocument);
elements.sourceUpload.addEventListener("change", inspectUploadedDocument);
elements.resumeImportsInProgressList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-resume-review-id]");
  if (!button) return;
  void continueResumeReview(button.dataset.resumeReviewId);
});
elements.libraryResumeFile.addEventListener("change", () => {
  const file = elements.libraryResumeFile.files[0];
  if (file && !elements.libraryResumeDisplayName.value.trim()) {
    elements.libraryResumeDisplayName.value = file.name.replace(/\.[^.]+$/, "");
  }
});
elements.saveLibraryResumeButton.addEventListener("click", saveLibraryResume);
elements.resumeIntentInputs.forEach((input) => {
  input.addEventListener("change", () => {
    if (input.checked) setResumeIntent(input.value);
  });
  input.addEventListener("keydown", (event) => {
    const offset = {
      ArrowLeft: -1,
      ArrowUp: -1,
      ArrowRight: 1,
      ArrowDown: 1,
    }[event.key];
    if (offset === undefined) return;
    event.preventDefault();
    const currentIndex = elements.resumeIntentInputs.indexOf(input);
    const nextIndex = (currentIndex + offset + elements.resumeIntentInputs.length)
      % elements.resumeIntentInputs.length;
    const nextInput = elements.resumeIntentInputs[nextIndex];
    selectResumeIntent(nextInput);
    nextInput.focus();
  });
});
setResumeIntent(elements.resumeIntentInputs.find((input) => input.checked)?.value || "vault");
elements.loadMoreSourcesButton.addEventListener("click", loadMoreSources);
elements.loadMoreJobsButton.addEventListener("click", loadMoreJobs);
elements.jobSourceFile.addEventListener("change", loadJobSourceFile);
elements.jobSourceText.addEventListener("input", () => {
  updateJobCharacterCount();
  resetJobIntakeIdempotency();
});
[elements.jobSourceName, elements.jobCompany, elements.jobTitle, elements.jobSourceUrl, elements.jobPostingId]
  .forEach((input) => input.addEventListener("input", resetJobIntakeIdempotency));
elements.createJobButton.addEventListener("click", () => createJobDraft("captured"));
elements.createAppliedJobButton.addEventListener("click", () => createJobDraft("applied"));
elements.jobDraftSelect.addEventListener("change", (event) => selectJob(event.target.value));
elements.loadJobSourceButton.addEventListener("click", loadSelectedJobSource);
elements.jobInstructionList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-job-instruction-action]");
  if (!button) return;
  void saveJobInstructionState(
    button.dataset.jobInstructionFingerprint,
    button.dataset.jobInstructionAction,
  );
});
elements.jobContactCandidateList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-save-contact-candidate]");
  if (!button) return;
  void openContactCandidateConfirmation({
    sourceKind: button.dataset.contactSourceKind,
    sourceId: button.dataset.contactSourceId,
    candidateId: button.dataset.contactCandidateId,
    contentSha256: button.dataset.contactContentSha256,
  }, { trigger: button });
});
elements.addSelectedJobInstruction.addEventListener("click", () => {
  void addSelectedJobInstruction();
});
elements.reanalyzeJobButton.addEventListener("click", () => reanalyzeSelectedJob());
elements.jobWorkspaceTabButtons.forEach((button, index) => {
  button.addEventListener("click", () => setJobWorkspaceTab(button.dataset.jobWorkspaceTab));
  button.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const offset = event.key === "ArrowRight" ? 1 : -1;
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? elements.jobWorkspaceTabButtons.length - 1
        : (index + offset + elements.jobWorkspaceTabButtons.length)
          % elements.jobWorkspaceTabButtons.length;
    const next = elements.jobWorkspaceTabButtons[nextIndex];
    setJobWorkspaceTab(next.dataset.jobWorkspaceTab, { focus: true });
  });
});
elements.editJobDetailsButton.addEventListener("click", openJobEditForm);
elements.cancelJobEditButton.addEventListener("click", () => hideJobEditForm());
elements.saveJobEditButton.addEventListener("click", saveJobDetails);
elements.editJobGatesButton.addEventListener("click", openJobGatesEditor);
elements.cancelJobGatesButton.addEventListener("click", () => setJobGateEditing(false));
elements.saveJobGatesButton.addEventListener("click", saveJobGates);
elements.editJobRequirementsButton.addEventListener("click", openJobRequirementsEditor);
elements.cancelJobRequirementsButton.addEventListener("click", () => setJobRequirementsEditing(false));
elements.saveJobRequirementsButton.addEventListener("click", saveJobRequirements);
elements.addJobSkillButton.addEventListener("click", () => {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (job?.valid) void openSkillQuickAction("skill", { jobId: job.id });
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (state.jobIdentityEditing) hideJobEditForm();
  if (state.jobGatesEditing) setJobGateEditing(false);
  if (state.jobRequirementsEditing) setJobRequirementsEditing(false);
});
document.querySelector("#view-jobs").addEventListener("click", (event) => {
  const button = event.target.closest("[data-assessment-finding-key]");
  if (button) openAssessmentResolution(button);
});
elements.assessmentResolutionForm.addEventListener("submit", saveAssessmentResolution);
elements.closeAssessmentResolutionDialog.addEventListener("click", closeAssessmentResolution);
elements.cancelAssessmentResolutionButton.addEventListener("click", closeAssessmentResolution);
elements.correctAssessmentFactsButton.addEventListener("click", correctAssessmentFacts);
const refreshApplicationLedgerView = () => {
  renderApplicationLedger();
  renderJobApplication();
};
window.addEventListener("careerhub:applicationoutcomeschanged", () => {
  clearApplicationEmailUpdates();
  if (state.activeView === "applications") void loadApplicationEmailUpdates();
});
window.addEventListener("careerhub:opencontacts", (event) => {
  selectContact(event.detail?.contactId || null);
  switchView("contacts");
});
window.addEventListener("careerhub:openjob", (event) => {
  const jobId = event.detail?.jobId;
  if (!jobId) return;
  state.selectedJobId = jobId;
  switchView("job-library");
  void (async () => {
    await loadJobs({ force: true, autoReanalyze: false });
    await selectJob(jobId, { autoReanalyze: false });
  })();
});
window.addEventListener("careerhub:opendiscovery", (event) => {
  const discoveryId = event.detail?.discoveryId || null;
  state.jobFeedDiscoveryId = discoveryId;
  state.jobFeedIntent = null;
  setDiscoveryRouteTarget(discoveryId, null);
  switchView("job-feed");
});

window.addEventListener("careerhub:opendiagnostics", (event) => {
  const runId = String(event.detail?.runId || "").trim();
  if (runId) {
    state.selectedRunId = runId;
    state.logSelectionPinned = true;
  }
  void navigateToView("diagnostics");
});
window.addEventListener(CLIENT_DIAGNOSTIC_EVENT, (event) => {
  const events = event.detail?.events;
  mergeDiagnosticEvents(events);
  const runId = String(events?.[0]?.run_id || "").trim();
  if (runId) {
    state.selectedRunId = runId;
    state.logSelectionPinned = true;
  }
  if (state.activeView === "diagnostics") renderDiagnostics();
});
elements.applicationLedgerSearch.addEventListener("input", refreshApplicationLedgerView);
elements.applicationLedgerStatusFilter.addEventListener("change", refreshApplicationLedgerView);
document.querySelectorAll("[data-application-scope]").forEach((button) => {
  button.addEventListener("click", () => {
    state.applicationLedgerScope = button.dataset.applicationScope;
    refreshApplicationLedgerView();
  });
});
elements.applicationLedgerList.addEventListener("click", (event) => {
  const button = event.target.closest(".application-open-button");
  if (button) {
    selectApplicationJob(button.dataset.applicationJobId);
    return;
  }
  if (event.target.closest("a, select, button")) return;
  const row = event.target.closest("[data-application-job-id]");
  if (row) selectApplicationJob(row.dataset.applicationJobId);
});
elements.applicationLedgerList.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || event.target.matches("select, a, button")) return;
  const row = event.target.closest("[data-application-job-id]");
  if (row) selectApplicationJob(row.dataset.applicationJobId);
});
elements.applicationLedgerList.addEventListener("change", (event) => {
  const select = event.target.closest(".application-inline-status");
  if (select) saveInlineApplicationStatus(select);
});
elements.applicationEmailUpdatesList.addEventListener("change", (event) => {
  const select = event.target.closest("[data-email-update-job]");
  if (!select) return;
  const button = select
    .closest(".application-email-update-linker")
    ?.querySelector("[data-email-update-link]");
  if (button) button.disabled = !select.value;
});
elements.applicationEmailUpdatesList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-email-update-link]");
  if (button) void linkApplicationEmailUpdate(button);
});
elements.applicationEmailUpdatesMore.addEventListener("click", () => {
  void loadApplicationEmailUpdates({ append: true });
});
elements.applicationSessionReviewList.addEventListener("click", (event) => {
  const details = event.target.closest("[data-application-session-job-id]");
  if (details) {
    void selectApplicationJob(details.dataset.applicationSessionJobId);
    return;
  }
  const actionButton = event.target.closest("[data-application-session-action]");
  if (!actionButton) return;
  const row = actionButton.closest("[data-application-session-id]");
  if (!row) return;
  void resolveApplicationSessionReview(
    row.dataset.applicationSessionId,
    actionButton.dataset.applicationSessionAction,
    actionButton.dataset.sessionRevision,
  );
});
elements.jobLibraryApplicationBar.addEventListener("click", (event) => {
  if (event.target.closest("[data-open-selected-application]")) {
    openSelectedApplication();
  }
});
elements.applicationDrawerBackdrop.addEventListener("click", closeApplicationDrawer);
elements.closeApplicationDrawer.addEventListener("click", closeApplicationDrawer);
elements.openApplicationJobButton.addEventListener("click", openSelectedJobInLibrary);
elements.startApplicationSessionButton.addEventListener("click", startAttendedApplicationSession);
elements.cancelApplicationSessionButton.addEventListener("click", cancelAttendedApplicationSession);
elements.openResumeMaterialButton.addEventListener("click", () => openApplicationMaterials("resume"));
elements.openCoverLetterButton.addEventListener("click", () => openApplicationMaterials("cover-letter"));
elements.saveResumePlanButton.addEventListener("click", () => saveCurrentResumePlan(false));
elements.approveResumePlanButton.addEventListener("click", () => saveCurrentResumePlan(true));
elements.resumePlanWorkspace.addEventListener("change", handleResumePlanChange);
elements.resumePlanWorkspace.addEventListener("click", (event) => {
  const decisionButton = event.target.closest("[data-plan-experience-decision]");
  if (decisionButton) {
    setResumePlanExperienceDecision(decisionButton);
    return;
  }
  const moveButton = event.target.closest("[data-plan-section-move]");
  if (moveButton) moveResumePlanSection(moveButton);
});
elements.applicationResumeFile.addEventListener("change", () => {
  const file = elements.applicationResumeFile.files[0];
  if (file && !elements.applicationResumeDisplayName.value.trim()) {
    elements.applicationResumeDisplayName.value = file.name.replace(/\.[^.]+$/, "");
  }
});
elements.clearApplicationResumeSelectionButton.addEventListener("click", () => {
  void selectApplicationResume(null);
});
elements.recordSubmittedResumeButton.addEventListener("click", recordSubmittedResume);
elements.openApplicationResume.addEventListener("click", (event) => {
  void openSubmittedResumeFile(event, elements.applicationResumeMessage);
});
elements.openSubmittedResumeSummary.addEventListener("click", (event) => {
  void openSubmittedResumeFile(event, elements.applicationMessage);
});
elements.attachApplicationResumeButton.addEventListener("click", attachApplicationResume);
elements.applicationStatusForm.addEventListener("submit", saveApplicationStatus);
elements.materialEditorBackButton.addEventListener("click", () => returnFromMaterialEditor());
elements.materialTabButtons.forEach((button) => {
  button.addEventListener("click", () => { void activateMaterialTab(button.dataset.materialTab); });
  button.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const currentIndex = elements.materialTabButtons.indexOf(button);
    const offset = event.key === "ArrowRight" ? 1 : -1;
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? elements.materialTabButtons.length - 1
        : (currentIndex + offset + elements.materialTabButtons.length)
          % elements.materialTabButtons.length;
    const next = elements.materialTabButtons[nextIndex];
    void activateMaterialTab(next.dataset.materialTab).then(() => next.focus());
  });
});
elements.resumeMaterialStepButtons.forEach((button, index) => {
  button.addEventListener("click", () => {
    void activateResumeMaterialStep(button.dataset.resumeMaterialStep);
  });
  button.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
    event.preventDefault();
    const offset = event.key === "ArrowRight" ? 1 : -1;
    const next = elements.resumeMaterialStepButtons[
      (index + offset + elements.resumeMaterialStepButtons.length)
      % elements.resumeMaterialStepButtons.length
    ];
    void activateResumeMaterialStep(next.dataset.resumeMaterialStep, { focus: true });
  });
});
elements.resumeMaterialStepBack.addEventListener("click", () => { void retreatResumeMaterialStep(); });
elements.resumeMaterialStepNext.addEventListener("click", () => { void advanceResumeMaterialStep(); });
elements.materialReturnToResumeSelection.addEventListener("click", () => (
  returnFromMaterialEditor({ selectResume: true })
));
elements.copyResumeAiCommand.addEventListener("click", copyResumeAiCommand);
elements.saveResumeMaterialButton.addEventListener("click", () => saveResumeMaterial(false));
elements.approveResumeMaterialButton.addEventListener("click", () => saveResumeMaterial(true));
elements.rebuildResumeMaterialButton.addEventListener("click", rebuildResumeMaterialFromPlan);
elements.resumeMaterialForm.addEventListener("input", handleResumeMaterialInput);
elements.resumeMaterialExportButtons.forEach((button) => {
  button.addEventListener("click", () => downloadResumeMaterial(button.dataset.resumeMaterialExport));
});
elements.copyCoverLetterAiCommand.addEventListener("click", copyCoverLetterAiCommand);
elements.refreshCoverLetterSources.addEventListener("click", refreshCoverLetterSources);
elements.addCoverLetterEvidenceButton.addEventListener("click", () => addCoverLetterEvidence());
elements.coverLetterForm.addEventListener("input", handleCoverLetterInput);
elements.coverLetterEvidenceSearch.addEventListener("input", (event) => {
  state.materialEvidenceSearch = event.target.value;
  renderCoverLetterCatalog();
});
elements.coverLetterEvidenceCatalog.addEventListener("click", (event) => {
  const button = event.target.closest("[data-add-cover-letter-evidence]");
  if (button) addCoverLetterEvidence(button.dataset.addCoverLetterEvidence);
});
elements.coverLetterEvidenceItems.addEventListener("click", (event) => {
  const button = event.target.closest("[data-remove-cover-letter-evidence]");
  if (button) removeCoverLetterEvidence(Number(button.dataset.removeCoverLetterEvidence));
});
elements.coverLetterExportButtons.forEach((button) => {
  button.addEventListener("click", () => downloadCoverLetter(button.dataset.coverLetterExport));
});

elements.reviewBackButton.addEventListener("click", () => { void navigateToView("intake"); });
elements.buildReviewButton.addEventListener("click", buildReview);
elements.suggestCardsButton.addEventListener("click", suggestCards);
elements.acceptSuggestionsButton.addEventListener("click", acceptSuggestions);
elements.acceptCardButton.addEventListener("click", acceptSelectedCardSuggestions);

elements.saveDraftButton.addEventListener("click", () => saveDraft(false));
elements.prepareApprovalButton.addEventListener("click", prepareApprovalPreview);
elements.closeApprovalButton.addEventListener("click", closeApprovalPreview);
elements.saveApprovalDecisionsButton.addEventListener("click", saveApprovalDecisions);
elements.applyApprovalButton.addEventListener("click", applyApprovalDecisions);
elements.keepApprovedResumeButton.addEventListener("click", keepApprovedImportForApplications);
elements.removeApprovedImportButton.addEventListener("click", removeApprovedImportSource);
elements.approvalPreviewList.addEventListener("change", (event) => {
  const select = event.target.closest(".approval-decision-select");
  if (!select) return;
  setApprovalDecision(
    select.dataset.approvalSubjectKind,
    select.dataset.approvalSubjectId,
    select.value
  );
  setReviewMessage("Approval decision changed. Save decisions to persist it.");
  renderApprovalPreview();
});
elements.approvalPreviewList.addEventListener("click", (event) => {
  const useAll = event.target.closest("[data-approval-use-all]");
  if (useAll) {
    useAllApprovalSourceFields(useAll.dataset.approvalUseAll);
    setReviewMessage("All available source fields selected. Save decisions to persist them.");
    renderApprovalPreview();
    return;
  }
  const fieldChoice = event.target.closest("[data-approval-field-choice]");
  if (!fieldChoice || fieldChoice.disabled) return;
  setApprovalFieldResolution(
    fieldChoice.dataset.approvalFieldSubject,
    fieldChoice.dataset.approvalField,
    fieldChoice.dataset.approvalFieldChoice
  );
  setReviewMessage("Field decision changed. Save decisions to persist it.");
  renderApprovalPreview();
});
elements.deleteCardButton.addEventListener("click", moveSelectedCardToReview);
elements.restoreCardButton.addEventListener("click", () => restoreCard(state.selectedEntityId));
elements.ungroupCardButton.addEventListener("click", () => ungroupCard(state.selectedEntityId));
elements.entityHierarchy.addEventListener("click", (event) => {
  const card = event.target.closest("[data-entity-id]");
  if (!card) return;
  state.selectedEntityId = card.dataset.entityId;
  state.blockFilter = "current";
  setReviewInspectorOpen(true);
  renderReview();
});
elements.closeReviewInspector.addEventListener("click", () => setReviewInspectorOpen(false));
elements.reviewInspectorBackdrop.addEventListener("click", () => setReviewInspectorOpen(false));
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && state.reviewInspectorOpen) {
    setReviewInspectorOpen(false);
  }
});
elements.cardEditor.addEventListener("input", (event) => {
  const field = event.target.dataset.cardField;
  const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
  if (!field || !entity) return;
  const listFields = new Set(["technologies", "emails", "phones", "links", "honors", "items"]);
  const rawItems = field === "items" && ["project", "certification"].includes(entity.entity_type)
    ? event.target.value.split(/\r?\n/)
    : event.target.value.split(",");
  entity[field] = listFields.has(field)
    ? [...new Set(rawItems.map((item) => item.trim()).filter(Boolean))]
    : event.target.value || (field === "user_note" ? null : "");
  markReviewDirty();
  renderEntityHierarchy();
});
elements.cardEditor.addEventListener("click", (event) => {
  const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
  if (!entity) return;
  const addButton = event.target.closest("[data-add-review-skills]");
  if (addButton) {
    const input = addButton.closest("[data-review-skills-entity]")?.querySelector("[data-review-skill-input]");
    if (input && addReviewSkills(entity, input.value)) input.value = "";
    return;
  }
  const removeSkill = event.target.closest("[data-remove-review-skill-id]");
  if (removeSkill) {
    entity.technologies = (entity.technologies || []).filter(
      (skillId) => skillId !== removeSkill.dataset.removeReviewSkillId
    );
    markReviewDirty();
    renderEntityHierarchy();
    renderCardEditor();
    return;
  }
  const removePending = event.target.closest("[data-remove-pending-skill]");
  if (removePending) {
    entity.pending_skills = (entity.pending_skills || []).filter(
      (_, index) => index !== Number(removePending.dataset.removePendingSkill)
    );
    markReviewDirty();
    renderEntityHierarchy();
    renderCardEditor();
  }
});
elements.cardEditor.addEventListener("keydown", (event) => {
  const input = event.target.closest("[data-review-skill-input]");
  if (!input || !["Enter", "Tab", ","].includes(event.key) || !input.value.trim()) return;
  const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
  if (!entity) return;
  event.preventDefault();
  if (addReviewSkills(entity, input.value)) input.value = "";
});
elements.cardEditor.addEventListener("change", (event) => {
  const select = event.target.closest("[data-pending-skill-group]");
  if (!select) return;
  const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
  const index = Number(select.dataset.pendingSkillGroup);
  const pending = entity?.pending_skills?.[index];
  if (!pending) return;
  if (select.value === "__create_group__") {
    select.value = pending.primary_group_id || state.skillsRegistry?.default_custom_group_id || "";
    void openSkillQuickAction("group", {
      reviewEntityId: entity.id,
      pendingSkillIndex: index,
    });
    return;
  }
  pending.primary_group_id = select.value;
  markReviewDirty();
});
elements.reviewBlockList.addEventListener("click", (event) => {
  const editButton = event.target.closest("[data-edit-queued-card]");
  const restoreButton = event.target.closest("[data-restore-card]");
  const ungroupButton = event.target.closest("[data-ungroup-card]");
  if (editButton) {
    state.selectedEntityId = editButton.dataset.editQueuedCard;
    state.blockFilter = "current";
    setReviewInspectorOpen(true);
    renderReview();
  } else if (restoreButton) {
    restoreCard(restoreButton.dataset.restoreCard);
  } else if (ungroupButton) {
    ungroupCard(ungroupButton.dataset.ungroupCard);
  }
});
elements.reviewBlockList.addEventListener("change", (event) => {
  const categorySelect = event.target.closest(".category-select");
  const ownerSelect = event.target.closest(".owner-select");
  const select = categorySelect || ownerSelect;
  if (!select) return;
  const review = currentReview();
  const item = review?.block_reviews.find(
    (reviewItem) => reviewItem.source_ref.block_id === select.dataset.blockId
  );
  if (!review || !item) return;
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));

  if (categorySelect) {
    if (select.value === "__structural") {
      item.disposition = "structural";
      item.owner_id = null;
      item.suggested_owner_ids = [];
    } else if (select.value === "__ignored") {
      item.disposition = "ignored";
      item.owner_id = null;
      item.suggested_owner_ids = [];
    } else {
      item.disposition = "unassigned";
      item.category = select.value || null;
      const owner = item.owner_id ? entityById.get(item.owner_id) : null;
      if (!owner || entityCategory(owner) !== item.category) item.owner_id = null;
      item.suggested_owner_ids = item.suggested_owner_ids.filter((entityId) => {
        const entity = entityById.get(entityId);
        return entity && (!item.category || entityCategory(entity) === item.category);
      });
      if (item.owner_id) item.disposition = "assigned";
    }
  } else if (!select.value) {
    item.disposition = "unassigned";
    item.owner_id = null;
  } else if (select.value === "__category__") {
    item.disposition = "categorized";
    item.owner_id = null;
    item.suggested_owner_ids = [];
  } else {
    const owner = entityById.get(select.value);
    if (!owner) return;
    item.disposition = "assigned";
    item.owner_id = owner.id;
    item.category = entityCategory(owner);
    item.suggested_owner_ids = [];
  }

  const scrollTop = elements.reviewBlockList.scrollTop;
  markReviewDirty();
  renderReview();
  elements.reviewBlockList.scrollTop = scrollTop;
});
document.querySelectorAll("[data-block-filter]").forEach((button) => {
  button.addEventListener("click", () => {
    state.blockFilter = button.dataset.blockFilter;
    document.querySelectorAll("[data-block-filter]").forEach((item) => {
      item.classList.toggle("active", item === button);
    });
    renderReviewBlocks();
  });
});
elements.runSelect.addEventListener("change", (event) => {
  state.selectedRunId = event.target.value;
  state.logSelectionPinned = true;
  renderDiagnostics();
});
async function openResumeReview(sourceId) {
  if (!sourceId) return;
  if (!(await confirmReviewNavigation("review", sourceId))) return;
  const reviewEpoch = beginReviewLoad(sourceId);
  switchView("review");
  await loadReview(sourceId, { epoch: reviewEpoch });
}

async function loadRuntimeInfo() {
  try {
    state.runtimeInfo = await api.getRuntimeInfo();
    const profile = state.runtimeInfo.data_profile || "external";
    const channel = state.runtimeInfo.channel || "unknown";
    const dataRoot = state.runtimeInfo.data_root || "Unavailable";
    const appVersion = state.runtimeInfo.app_version || "unknown";
    elements.runtimeDataProfile.textContent = `Data: ${profile} · ${channel}`;
    elements.runtimeDataRoot.textContent = dataRoot;
    elements.runtimeStorageNote.title = `Active Career Hub Data Home: ${dataRoot}`;
    elements.appVersion.textContent = appVersion === "unknown" ? "v—" : `v${appVersion}`;
    elements.appVersion.setAttribute(
      "aria-label",
      appVersion === "unknown" ? "Application version unavailable" : `Career Hub version ${appVersion}`,
    );
    const security = state.runtimeInfo.platform_security || {};
    const credentialStore = security.credential_store || {};
    const documentEncryption = security.document_encryption || {};
    elements.emailAppPasswordHelp.textContent = credentialStore.storage_copy
      || "Protected credential storage status is unavailable.";
    elements.emailSecurityHelp.textContent = credentialStore.help_copy
      || "Protected credential storage status is unavailable.";
    elements.documentSecurityCopy.textContent = documentEncryption.storage_copy
      || "Document encryption status is unavailable.";
  } catch (error) {
    elements.runtimeDataProfile.textContent = "Data Home unavailable";
    elements.runtimeDataRoot.textContent = formatRequestError(error);
    elements.appVersion.textContent = "v—";
    elements.appVersion.setAttribute("aria-label", "Application version unavailable");
  }
}

async function initialize() {
  try {
    initializeConnectivity();
    initDocuments();
    initDiscoveries();
    initContacts();
    initApplicationHelperConfiguration();
    initDesktopUpdates();
    initDesktopBackups();
    initDesktopAgent();
    initExtensionPairing(api);
    loadSkillsHubModePreference();
    loadSkillsRegistryLayoutPreference();
    loadCareerVaultLayoutPreference();
    loadDemandGapsPeriodPreference();
    await loadRuntimeInfo();
    await restoreLocalRoute();
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

window.addEventListener("popstate", () => { void restoreLocalRoute(); });
if ("scrollRestoration" in window.history) window.history.scrollRestoration = "manual";
window.addEventListener("beforeunload", (event) => {
  if (!state.reviewDirty && !state.materialEditorDirty
      && !state.resumeMaterialDirty && !state.resumePlanDirty) return;
  event.preventDefault();
  event.returnValue = "";
});
initializeButtonTooltips();
initialize();
