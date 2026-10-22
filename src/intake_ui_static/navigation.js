/**
 * Navigation is data, not page markup. Primary destinations stay stable and
 * each domain exposes the same preparation entry points regardless of how the
 * user arrived there.
 */

const JOB_WORKFLOW_ITEMS = [
  {
    view: "job-intake",
    shortLabel: "+",
    label: "Add job",
    tooltip: "Add a complete job posting and analyze it locally.",
    workflow: {
      title: "Add job",
      description: "Save a complete posting and create a structured local job record.",
      state: "Jobs / Add",
    },
  },
  {
    view: "job-feed",
    shortLabel: "JB",
    label: "Jobs",
    tooltip: "Review opportunities collected from enabled sources.",
    workflow: {
      title: "Jobs",
      description: "Review and triage opportunities from every enabled source.",
      state: "Jobs",
    },
  },
  {
    view: "job-library",
    shortLabel: "JW",
    label: "Job Workspace",
    tooltip: "Prepare one selected job and its application materials.",
    workflow: {
      title: "Job Workspace",
      description: "Analyze one selected job and prepare truthful application materials.",
      state: "Jobs / Workspace",
    },
  },
  {
    view: "applications",
    shortLabel: "AP",
    label: "Applications",
    tooltip: "Update application stages and review status history.",
    workflow: {
      title: "Applications",
      description: "Filter saved applications, update stages, and inspect status history.",
      state: "Applications",
    },
  },
  {
    view: "demand-gaps",
    shortLabel: "IN",
    label: "Insights",
    tooltip: "Compare job demand with evidence recorded in Career Vault.",
    workflow: {
      title: "Insights",
      description: "See what saved jobs request and how well Career Vault documents it.",
      state: "Insights",
    },
  },
];

const PRIMARY_ITEMS = [
  JOB_WORKFLOW_ITEMS[1],
  JOB_WORKFLOW_ITEMS[3],
  {
    view: "resume-library",
    shortLabel: "RS",
    label: "Resumes",
    tooltip: "Manage reusable resume versions for applications.",
  },
  {
    view: "vault",
    shortLabel: "CV",
    label: "Career Vault",
    tooltip: "Manage verified career facts and supporting evidence.",
  },
  JOB_WORKFLOW_ITEMS[4],
];

const ADD_RESUME_ITEM = {
  view: "intake",
  shortLabel: "+",
  label: "Add resume",
  tooltip: "Add a local resume for Career Vault or applications.",
};

const CONTEXT_ITEMS = Object.freeze({
  jobs: [
    {
      view: "job-feed",
      shortLabel: "JF",
      label: "Job Feed",
      tooltip: "Review and triage opportunities from every enabled source.",
    },
    JOB_WORKFLOW_ITEMS[2],
  ],
  resumes: [
    {
      view: "resume-library",
      shortLabel: "RL",
      label: "Resume Library",
      tooltip: "Manage reusable resume versions for applications.",
    },
    {
      view: "intake",
      shortLabel: "RD",
      label: "Resume digest",
      tooltip: "Add or continue a resume digest for Career Vault.",
    },
  ],
  vault: [
    { view: "vault", shortLabel: "EX", label: "Experience", tooltip: "Manage verified experience records." },
    { view: "profile", shortLabel: "PF", label: "Profile", tooltip: "Review candidate identity and contact details." },
    { view: "projects", shortLabel: "PR", label: "Projects", tooltip: "Manage projects and their evidence links." },
    { view: "skills", shortLabel: "SK", label: "Skills", tooltip: "Manage reusable skills and evidence." },
    { view: "education", shortLabel: "ED", label: "Education", tooltip: "Manage schools and certifications." },
    { view: "documents", shortLabel: "DC", label: "Documents", tooltip: "Manage encrypted local identity and authorization documents." },
    { view: "contacts", shortLabel: "CT", label: "Contacts", tooltip: "Review explicitly saved recruiting contacts." },
  ],
});

const MORE_ITEMS = [
  { view: "contacts", shortLabel: "CT", label: "Contacts", tooltip: "Review explicitly saved recruiting contacts." },
  { view: "documents", shortLabel: "DC", label: "Documents", tooltip: "Manage encrypted local identity and authorization documents." },
  { view: "diagnostics", shortLabel: "DX", label: "Diagnostics", tooltip: "Inspect local processing events, errors, and timings." },
];

export const NAVIGATION_CONFIG = [
  { id: "workspace", label: "Workspace", items: PRIMARY_ITEMS },
  { id: "more", label: "More", items: MORE_ITEMS },
];

export const NAVIGATION_UTILITY_ITEMS = MORE_ITEMS;

export function navigationViewFor(viewName) {
  if (["job-intake", "job-feed", "job-library", "application-materials"].includes(viewName)) {
    return "job-feed";
  }
  if (["intake", "review", "resume-library"].includes(viewName)) return "resume-library";
  if (["profile", "vault", "projects", "skills", "education", "documents", "contacts"].includes(viewName)) return "vault";
  return viewName;
}

export function jobWorkflowCopy(viewName) {
  const item = JOB_WORKFLOW_ITEMS.find((candidate) => candidate.view === viewName);
  return item?.workflow || JOB_WORKFLOW_ITEMS[0].workflow;
}

export function isJobWorkflowView(viewName) {
  return JOB_WORKFLOW_ITEMS.some((item) => item.view === viewName);
}

const RETRO_ICONS = {
  jobs: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 3h5l2 2h7v2H1V3z" fill="#b09562"/><path d="M3 4h9v4H3z" fill="#edf1f2"/><path d="M1 6h14v7H1z" fill="#d4b886"/><path d="M1 6v7h14V6H1zm13 6H2V7h12v5z" fill="#243036" opacity="0.3"/><path d="M1 6h14v1H1z" fill="#e8d4a7"/></svg>',
  "job-feed": '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 3h5l2 2h7v2H1V3z" fill="#9ea273"/><path d="M3 4h9v4H3z" fill="#edf1f2"/><path d="M1 6h14v7H1z" fill="#b8be88"/><path d="M1 6h14v1H1z" fill="#dce2b6"/><path d="M4 8h8v1H4zm0 2h5v1H4z" fill="#3a4d38"/></svg>',
  "job-library": '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 1h8l4 4v10H2V1z" fill="#edf1f2"/><path d="M10 1v4h4" fill="#a8b8bf"/><path d="M4 6h6v1H4zm0 2h6v1H4zm0 2h4v1H4z" fill="#7a8d96"/><path d="M8 11l5-5 2 2-5 5-2-1z" fill="#88cfc0"/><path d="M7 13l1-2 1 1-2 1z" fill="#243036"/></svg>',
  applications: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M3 2h10v13H3z" fill="#7e9aa6"/><path d="M6 1h4v2H6z" fill="#243036"/><path d="M7 1h2v1H7z" fill="#edf1f2"/><path d="M4 3h8v11H4z" fill="#f4f7f8"/><path d="M5 5h1v1H5zm2 0h4v1H7zm-2 3h1v1H5zm2 0h4v1H7zm-2 3h1v1H5zm2 0h4v1H7z" fill="#60727a"/><path d="M5 5l.7.7L7 4.4" stroke="#48a896" stroke-width="1.2" fill="none"/><path d="M5 8l.7.7L7 7.4" stroke="#48a896" stroke-width="1.2" fill="none"/></svg>',
  resumes: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 1h8l4 4v10H2V1z" fill="#f0f4f6"/><path d="M10 1v4h4" fill="#b0c0c7"/><path d="M2 1v14h12V5l-4-4H2zm1 1h6v4h4v8H3V2z" fill="#34434a" opacity="0.35"/><path d="M4 4h5v2H4z" fill="#4d8c9e"/><path d="M4 7h8v1H4zm0 2h8v1H4zm0 2h6v1H4z" fill="#7a8d96"/></svg>',
  "resume-library": '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 1h8l4 4v10H2V1z" fill="#f0f4f6"/><path d="M10 1v4h4" fill="#b0c0c7"/><path d="M4 4h5v2H4z" fill="#4d8c9e"/><path d="M4 7h8v1H4zm0 2h8v1H4zm0 2h6v1H4z" fill="#7a8d96"/></svg>',
  intake: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 1h7l4 4v9H2V1z" fill="#edf1f2"/><path d="M9 1v4h4" fill="#b0c0c7"/><path d="M4 4h4v1H4zm0 2h3v1H4z" fill="#7a8d96"/><path d="M8 8h3v3h2l-3.5 4L6 11h2V8z" fill="#88cfc0"/><path d="M8 8h3v3h2l-3.5 4L6 11h2V8z" stroke="#243036" stroke-width="0.7" fill="none"/></svg>',
  vault: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 2h14v12H1V2z" fill="#586c75"/><path d="M2 3h12v10H2V3z" fill="#768c96"/><path d="M2 3h12v1H3v9H2V3z" fill="#9eb1ba"/><path d="M13 4v8H3v1h11V4h-1z" fill="#38474e"/><circle cx="8" cy="8" r="2.8" fill="#34434a"/><circle cx="8" cy="8" r="2" fill="#d9c08d"/><circle cx="8" cy="8" r="0.8" fill="#243036"/><path d="M1 4h1v2H1zm0 6h1v2H1z" fill="#9eb1ba"/></svg>',
  experience: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M6 2h4v2H6z" fill="none" stroke="#8c775a" stroke-width="1.2"/><path d="M1 4h14v10H1V4z" fill="#8f7450"/><path d="M1 7h14v1H1z" fill="#634f34"/><path d="M4 6h2v3H4zm6 0h2v3h-2z" fill="#e0c285"/><path d="M5 7h1v1H5zm6 0h1v1h-1z" fill="#243036"/></svg>',
  profile: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 3h14v10H1V3z" fill="#edf1f2"/><path d="M1 3v10h14V3H1zm13 9H2V4h12v8z" fill="#34434a" opacity="0.3"/><path d="M3 5h4v6H3V5z" fill="#8ea2ab"/><circle cx="5" cy="7" r="1.3" fill="#edf1f2"/><path d="M3.5 10.5a1.8 1.8 0 0 1 3 0v.5h-3v-.5z" fill="#edf1f2"/><path d="M8 6h5v1H8zm0 2h5v1H8zm0 2h3v1H8z" fill="#697e88"/></svg>',
  projects: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 3h14v10H1V3z" fill="#38647d"/><path d="M2 4h12v8H2V4z" fill="#467894"/><path d="M5 4v8M8 4v8M11 4v8M2 7h12M2 10h12" stroke="#689ab5" stroke-width="0.8"/><path d="M7 6l3 3-1 1-3-3z" fill="#d4b886"/><path d="M10 5l2 2-1 1-2-2z" fill="#a8b8bf"/></svg>',
  skills: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M5 9l-2 6 4-2 1 2V9H5zm3 0v6l1-2 4 2-2-6H8z" fill="#58857b"/><circle cx="8" cy="6" r="4.5" fill="#8ec5ba"/><circle cx="8" cy="6" r="3.2" fill="#d9c08d"/><path d="M8 4l.6 1.3 1.4.2-1 1 .2 1.4L8 7.3 6.8 8l.2-1.4-1-1 1.4-.2z" fill="#63522f"/></svg>',
  education: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M8 2L1 5l7 3 7-3-7-3z" fill="#3a4d57"/><path d="M8 3.3L3 5.4l5 2.1 5-2.1-5-2.1z" fill="#4d6470"/><path d="M4 6.8v3.2c0 1.5 1.8 2.5 4 2.5s4-1 4-2.5V6.8L8 8.5 4 6.8z" fill="#26343b"/><path d="M8 5.5l-5 1.5v3.5" stroke="#d9c08d" stroke-width="1" fill="none"/><circle cx="3" cy="11" r="1" fill="#d9c08d"/></svg>',
  documents: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 1h12v14H2V1z" fill="#f2ede4"/><path d="M2 1v14h12V1H2zm11 13H3V2h10v12z" fill="#34434a" opacity="0.25"/><path d="M4 3h8v1H4zm0 2h8v1H4zm0 2h5v1H4zm0 2h5v1H4z" fill="#7d7367"/><path d="M10 9l1 4-1.5-.8L8 13l.8-4h1.2z" fill="#a8524a"/><circle cx="10" cy="10" r="2.2" fill="#c45d54"/><circle cx="10" cy="10" r="1.3" fill="#e07b72"/></svg>',
  contacts: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 2h12v12H1V2z" fill="#3a5463"/><path d="M2 3h10v10H2V3z" fill="#4c6a7c"/><path d="M13 3h2v2h-2zm0 3h2v2h-2zm0 3h2v2h-2zm0 3h2v2h-2z" fill="#c27a72"/><path d="M13 6h2v2h-2z" fill="#d4b886"/><path d="M13 9h2v2h-2z" fill="#7ac4b4"/><circle cx="7" cy="6.5" r="1.8" fill="#edf1f2"/><path d="M4 11c0-1.7 1.3-2.5 3-2.5s3 .8 3 2.5v.5H4v-.5z" fill="#edf1f2"/></svg>',
  "demand-gaps": '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M1 14h14v1H1V14zM1 1v13h1V1H1z" fill="#475a63"/><path d="M3 9h2v4H3V9z" fill="#467894"/><path d="M3 9l1-1h2l-1 1H3z" fill="#689ab5"/><path d="M5 9l1-1v4l-1 1V9z" fill="#2d5266"/><path d="M7 6h2v7H7V6z" fill="#58857b"/><path d="M7 6l1-1h2l-1 1H7z" fill="#7ab3a7"/><path d="M9 6l1-1v7l-1 1V6z" fill="#3c5e56"/><path d="M11 3h2v10h-2V3z" fill="#88cfc0"/><path d="M11 3l1-1h2l-1 1h-2z" fill="#b1e5dc"/><path d="M13 3l1-1v10l-1 1V3z" fill="#569c8f"/></svg>',
  "job-intake": '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 2h12v12H2V2z" fill="#223138"/><path d="M2 2h12v1H3v10H2V2z" fill="#475a63"/><path d="M13 3v10H3v1h11V3h-1z" fill="#141d21"/><path d="M7 4h2v3h3v2H9v3H7V9H4V7h3V4z" fill="#88cfc0"/></svg>',
  add: '<svg class="nav-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M2 2h12v12H2V2z" fill="#223138"/><path d="M2 2h12v1H3v10H2V2z" fill="#475a63"/><path d="M13 3v10H3v1h11V3h-1z" fill="#141d21"/><path d="M7 4h2v3h3v2H9v3H7V9H4V7h3V4z" fill="#88cfc0"/></svg>',
};

function createRetroIconSvg(name) {
  const svgString = RETRO_ICONS[name] || RETRO_ICONS.add;
  const template = document.createElement("template");
  template.innerHTML = svgString.trim();
  const icon = template.content.firstElementChild;
  return icon?.namespaceURI === "http://www.w3.org/2000/svg" ? icon : null;
}

function createNavButton(item, { active = false, context = false, hasChildren = false } = {}) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = context ? "nav-item nav-context-item nav-child-item" : "nav-item";
  if (hasChildren) button.classList.add("nav-parent-item");
  button.dataset.view = item.view;
  button.dataset.tooltip = item.tooltip;
  button.classList.toggle("active", active);
  if (active) button.setAttribute("aria-current", "page");

  const iconName = item.icon || (item.view === "vault" && context ? "experience" : item.view);
  const icon = createRetroIconSvg(iconName);
  if (icon) {
    button.append(icon);
  } else if (item.shortLabel) {
    const shortLabel = document.createElement("span");
    shortLabel.className = "nav-step";
    shortLabel.textContent = item.shortLabel;
    button.append(shortLabel);
  }
  const label = document.createElement("span");
  label.className = "nav-item-label";
  label.textContent = item.label;
  button.append(label);
  return button;
}

function contextualItemsFor(viewName) {
  const primaryView = navigationViewFor(viewName);
  if (primaryView === "job-feed") return CONTEXT_ITEMS.jobs;
  if (primaryView === "resume-library") return CONTEXT_ITEMS.resumes;
  if (primaryView === "vault") return CONTEXT_ITEMS.vault;
  return [];
}

export function contextualLabelFor(primaryView) {
  if (primaryView === "job-feed") return "Jobs";
  if (primaryView === "resume-library") return "Resumes";
  return "Career Vault";
}

function contextItemForActiveView(item, activeViewName) {
  if (item.view === "intake" && activeViewName === "review") {
    return { ...item, view: "review" };
  }
  return item;
}

function renderAddMenu(activeViewName) {
  const details = document.createElement("details");
  details.className = "nav-add-menu";
  const summary = document.createElement("summary");
  const addIcon = createRetroIconSvg("add");
  if (addIcon) summary.append(addIcon);
  const summaryText = document.createElement("span");
  summaryText.textContent = "+ Add";
  summary.append(summaryText);
  summary.setAttribute("aria-label", "Open quick actions");
  const options = document.createElement("div");
  options.className = "nav-add-options";
  options.append(
    createNavButton(JOB_WORKFLOW_ITEMS[0], { active: activeViewName === "job-intake", context: true }),
    createNavButton(ADD_RESUME_ITEM, { active: activeViewName === "intake", context: true }),
  );
  details.append(summary, options);
  return details;
}

export function renderNavigation(root, activeViewName) {
  if (!root) return;
  const primaryView = navigationViewFor(activeViewName);
  root.replaceChildren();

  const mainList = document.createElement("div");
  mainList.className = "nav-main-list";

  PRIMARY_ITEMS.forEach((primaryItem) => {
    const isSectionActive = primaryItem.view === primaryView;
    const sectionChildren = contextualItemsFor(primaryItem.view);
    const hasChildren = sectionChildren.length > 0;

    const parentButton = createNavButton(primaryItem, {
      active: !hasChildren && isSectionActive,
      hasChildren,
    });
    if (hasChildren && isSectionActive) {
      parentButton.classList.add("is-section-active");
    }
    mainList.append(parentButton);

    if (isSectionActive && hasChildren) {
      const childList = document.createElement("div");
      childList.className = "nav-nested-list nav-subnav";
      sectionChildren.forEach((childItem) => {
        const contextualItem = contextItemForActiveView(childItem, activeViewName);
        const exactActive = contextualItem.view === activeViewName
          || (activeViewName === "application-materials" && contextualItem.view === "job-library");
        childList.append(createNavButton(contextualItem, {
          active: exactActive,
          context: true,
        }));
      });
      mainList.append(childList);
    }
  });

  const footerActions = document.createElement("div");
  footerActions.className = "nav-footer-actions";
  footerActions.append(renderAddMenu(activeViewName));

  root.append(mainList, footerActions);
}
