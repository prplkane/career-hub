"use strict";

function inspectCareerHubSelection(maximumCharacters) {
  const value = globalThis.getSelection?.()?.toString() || "";
  const normalized = value.replace(/\r\n?/g, "\n").trim();
  return {
    selection_chars: normalized.length,
    selection_too_large: normalized.length > maximumCharacters,
  };
}

function extractCareerHubSelection(maximumCharacters) {
  const value = globalThis.getSelection?.()?.toString() || "";
  const normalized = value.replace(/\r\n?/g, "\n").trim();
  if (!normalized) {
    return { ok: false, error_code: "CAPTURE_EMPTY" };
  }
  if (normalized.length > maximumCharacters) {
    return { ok: false, error_code: "CAPTURE_TOO_LARGE" };
  }
  return {
    ok: true,
    source_text: normalized,
    structured: { title: "", company: "", location: "", posting_id: "" },
    extractor: { version: 1, strategy: "selection" },
  };
}

function extractCareerHubVisiblePage(
  maximumCharacters,
  minimumCharacters,
  maximumDomNodes,
  maximumJsonLdCharacters,
) {
  const extractorVersion = 2;
  const maximumCandidateElements = 180;
  const maximumTitleOccurrences = 60;
  const excludedTags = new Set([
    "BUTTON",
    "FORM",
    "INPUT",
    "NOSCRIPT",
    "OPTION",
    "SCRIPT",
    "SELECT",
    "STYLE",
    "TEMPLATE",
    "TEXTAREA",
  ]);
  const emptyStructured = () => ({
    title: "",
    company: "",
    location: "",
    posting_id: "",
  });
  const compact = (value, maximum = maximumCharacters) => {
    if (typeof value !== "string") {
      return "";
    }
    const normalized = value.replace(/\s+/g, " ").trim();
    return normalized.length <= maximum ? normalized : "";
  };
  const normalizeLines = (value) => value
    .replace(/\r\n?/g, "\n")
    .split("\n")
    .map((line) => line.replace(/[\t\f\v ]+/g, " ").trim())
    .filter(Boolean)
    .join("\n")
    .trim();
  const normalizedTitle = (value) => compact(value, 500)
    .normalize("NFKC")
    .toLowerCase()
    .replace(/[\u2018\u2019]/g, "'")
    .replace(/[\s|·•\u2013\u2014_-]+/g, " ")
    .replace(/[()[\]{},:;]+/g, " ")
    .trim();
  const visibleElement = (element) => {
    if (!(element instanceof Element)) {
      return true;
    }
    if (
      excludedTags.has(element.tagName)
      || element.hidden
      || element.getAttribute("aria-hidden")?.toLowerCase() === "true"
    ) {
      return false;
    }
    let style;
    try {
      style = globalThis.getComputedStyle(element);
    } catch {
      return false;
    }
    if (
      style.display === "none"
      || style.visibility === "hidden"
      || style.visibility === "collapse"
      || style.opacity === "0"
    ) {
      return false;
    }
    if (style.display !== "contents" && element.getClientRects().length === 0) {
      return false;
    }
    return true;
  };
  const renderedText = (root, checkVisibility) => {
    const fragments = [];
    let characters = 0;
    let nodes = 0;
    let tooLarge = false;
    const append = (value) => {
      if (!value || tooLarge) {
        return;
      }
      characters += value.length;
      if (characters > maximumCharacters) {
        tooLarge = true;
        return;
      }
      fragments.push(value);
    };
    const stack = [{ closing: false, node: root }];
    while (stack.length && !tooLarge) {
      const current = stack.pop();
      if (!current) {
        continue;
      }
      if (current.closing) {
        append("\n");
        continue;
      }
      const node = current.node;
      nodes += 1;
      if (nodes > maximumDomNodes) {
        return { text: "", too_large: true };
      }
      if (node.nodeType === Node.TEXT_NODE) {
        append((node.nodeValue || "").replace(/\s+/g, " ").trim());
        append(" ");
        continue;
      }
      if (!(node instanceof Element) || excludedTags.has(node.tagName)) {
        continue;
      }
      if (checkVisibility && !visibleElement(node)) {
        continue;
      }
      if (node.tagName === "BR") {
        append("\n");
        continue;
      }
      let block = true;
      if (checkVisibility) {
        try {
          const display = globalThis.getComputedStyle(node).display;
          block = !display.startsWith("inline") && display !== "contents";
        } catch {
          block = true;
        }
      } else {
        block = !new Set([
          "A", "ABBR", "B", "CODE", "EM", "I", "LABEL", "SMALL",
          "SPAN", "STRONG", "SUB", "SUP",
        ]).has(node.tagName);
      }
      if (block) {
        append("\n");
        stack.push({ closing: true, node });
      }
      const children = Array.from(node.childNodes);
      for (let index = children.length - 1; index >= 0; index -= 1) {
        stack.push({ closing: false, node: children[index] });
      }
    }
    return {
      text: tooLarge ? "" : normalizeLines(fragments.join("")),
      too_large: tooLarge,
    };
  };
  const detachedHtmlText = (value) => {
    if (typeof value !== "string" || !value.trim()) {
      return { text: "", too_large: false };
    }
    const parsed = new DOMParser().parseFromString(value, "text/html");
    return renderedText(parsed.body, false);
  };
  const jobSignalCount = (value) => {
    const folded = value.toLowerCase();
    const signals = [
      "about the role",
      "job description",
      "qualifications",
      "requirements",
      "responsibilities",
      "what you will do",
      "what you'll do",
      "experience",
      "skills",
      "compensation",
    ];
    return signals.filter((signal) => folded.includes(signal)).length;
  };
  const looksLikeLogin = (value) => {
    const folded = value.toLowerCase();
    return (
      /\b(sign in|log in|forgot password|create account)\b/.test(folded)
      && jobSignalCount(value) < 2
    );
  };
  const usable = (result, requiredSignals = 1) => Boolean(
    !result.too_large
    && result.text.length >= minimumCharacters
    && jobSignalCount(result.text) >= requiredSignals
    && !looksLikeLogin(result.text)
  );
  const isHost = (hostname, domain) => (
    hostname === domain || hostname.endsWith(`.${domain}`)
  );
  const providerDefinitions = [
    {
      domains: ["indeed.com"],
      detailSelectors: [
        "#jobDescriptionText",
        '[data-testid="jobsearch-JobComponent-description"]',
        '[data-testid="jobDescriptionText"]',
        ".jobsearch-jobDescriptionText",
      ],
      titleSelectors: [
        '[data-testid="jobsearch-JobInfoHeader-title"]',
        ".jobsearch-JobInfoHeader-title",
        "h1",
      ],
    },
    {
      domains: ["linkedin.com"],
      detailSelectors: [
        "#job-details",
        ".jobs-description__content",
        ".jobs-description-content__text",
        '[data-testid="job-details"]',
      ],
      titleSelectors: [
        ".job-details-jobs-unified-top-card__job-title",
        ".jobs-unified-top-card__job-title",
        ".top-card-layout__title",
        "h1",
      ],
    },
    {
      domains: ["joinhandshake.com"],
      detailSelectors: [
        '[data-hook="job-description"]',
        '[data-testid="job-description"]',
        '[data-testid="job-posting-description"]',
      ],
      titleSelectors: [
        '[data-hook="job-title"]',
        '[data-testid="job-title"]',
        "h1",
      ],
    },
    {
      domains: ["meeboss.com"],
      detailSelectors: [
        "#job-description",
        ".job-description",
        '[data-testid="job-description"]',
        '[class*="jobDescription"]',
      ],
      titleSelectors: [
        '[data-testid="job-title"]',
        '[class*="jobTitle"]',
        "h1",
      ],
    },
    {
      domains: ["12twenty.com"],
      detailSelectors: [
        "#job-description",
        ".job-description",
        '[data-testid="job-description"]',
        '[class*="jobDescription"]',
      ],
      titleSelectors: [
        '[data-testid="job-title"]',
        '[class*="jobTitle"]',
        "h1",
      ],
    },
  ];
  const hostname = globalThis.location?.hostname?.toLowerCase().replace(/\.$/, "") || "";
  const provider = providerDefinitions.find((definition) => (
    definition.domains.some((domain) => isHost(hostname, domain))
  )) || null;
  const queryVisible = (selectors, limit = 100) => {
    const found = [];
    for (const selector of selectors) {
      let matches;
      try {
        matches = document.querySelectorAll(selector);
      } catch {
        continue;
      }
      for (const element of matches) {
        if (found.length >= limit) {
          return found;
        }
        if (visibleElement(element) && !found.includes(element)) {
          found.push(element);
        }
      }
    }
    return found;
  };
  const textForTitle = (element) => {
    const result = renderedText(element, true);
    return result.too_large ? "" : compact(result.text, 500);
  };
  const genericTitle = /^(careers?|find jobs?|job search|jobs?(?: for you)?|open roles?)$/i;
  const uniqueTitleFrom = (elements) => {
    const values = elements
      .map(textForTitle)
      .filter((value) => value && !genericTitle.test(value));
    const byNormalized = new Map();
    for (const value of values) {
      const key = normalizedTitle(value);
      if (key && !byNormalized.has(key)) {
        byNormalized.set(key, value);
      }
    }
    return byNormalized.size === 1 ? [...byNormalized.values()][0] : "";
  };
  const titleFromPage = () => {
    if (provider) {
      const providerTitle = uniqueTitleFrom(queryVisible(provider.titleSelectors, 30));
      if (providerTitle) {
        return providerTitle;
      }
    }
    return uniqueTitleFrom(queryVisible(["h1"], 30));
  };
  const normalizedType = (value) => {
    const values = Array.isArray(value) ? value : [value];
    return values.some((item) => (
      typeof item === "string" && item.toLowerCase() === "jobposting"
    ));
  };
  const findJobPostings = (value) => {
    const found = [];
    const queue = [{ depth: 0, value }];
    let visited = 0;
    while (queue.length && visited < 500) {
      const current = queue.shift();
      if (!current) {
        continue;
      }
      visited += 1;
      if (current.depth > 6 || current.value == null) {
        continue;
      }
      if (Array.isArray(current.value)) {
        for (const child of current.value.slice(0, 100)) {
          queue.push({ depth: current.depth + 1, value: child });
        }
        continue;
      }
      if (typeof current.value !== "object") {
        continue;
      }
      if (normalizedType(current.value["@type"])) {
        found.push(current.value);
      }
      for (const child of Object.values(current.value).slice(0, 100)) {
        if (child && typeof child === "object") {
          queue.push({ depth: current.depth + 1, value: child });
        }
      }
    }
    return found;
  };
  const organizationName = (value) => {
    if (typeof value === "string") {
      return compact(value, 500);
    }
    return compact(value?.name, 500);
  };
  const locationText = (value) => {
    const locations = Array.isArray(value) ? value : [value];
    for (const location of locations) {
      const address = location?.address || location;
      if (!address || typeof address !== "object") {
        continue;
      }
      const country = typeof address.addressCountry === "object"
        ? address.addressCountry?.name
        : address.addressCountry;
      const parts = [
        address.addressLocality,
        address.addressRegion,
        address.postalCode,
        country,
      ].map((item) => compact(item, 100)).filter(Boolean);
      const normalized = compact(parts.join(", "), 500);
      if (normalized) {
        return normalized;
      }
    }
    return "";
  };
  const identifierText = (value) => {
    if (typeof value === "string" || typeof value === "number") {
      return compact(String(value), 300);
    }
    return compact(String(value?.value || ""), 300);
  };
  let sawOversized = false;
  const pageTitle = titleFromPage();
  const jsonCandidates = [];
  let jsonCharacters = 0;
  const scripts = Array.from(
    document.querySelectorAll('script[type="application/ld+json"]'),
  ).slice(0, 50);
  for (const script of scripts) {
    const raw = script.textContent || "";
    jsonCharacters += raw.length;
    if (!raw.trim() || jsonCharacters > maximumJsonLdCharacters) {
      sawOversized = sawOversized || jsonCharacters > maximumJsonLdCharacters;
      break;
    }
    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch {
      continue;
    }
    for (const posting of findJobPostings(parsed)) {
      const description = detachedHtmlText(posting.description);
      if (description.too_large) {
        sawOversized = true;
        continue;
      }
      if (description.text.length < minimumCharacters || looksLikeLogin(description.text)) {
        continue;
      }
      jsonCandidates.push({
        source_text: description.text,
        structured: {
          title: compact(posting.title, 500),
          company: organizationName(posting.hiringOrganization),
          location: locationText(posting.jobLocation),
          posting_id: identifierText(posting.identifier),
        },
      });
    }
  }
  if (jsonCandidates.length) {
    const distinctJsonCandidates = [
      ...new Map(jsonCandidates.map((candidate) => [
        JSON.stringify([candidate.source_text, candidate.structured]),
        candidate,
      ])).values(),
    ];
    const documentTitle = normalizedTitle(document.title);
    const currentTitle = normalizedTitle(pageTitle);
    const titleMatches = distinctJsonCandidates.filter((candidate) => (
      candidate.structured.title
      && (
        normalizedTitle(candidate.structured.title) === currentTitle
        || documentTitle.includes(normalizedTitle(candidate.structured.title))
      )
    ));
    const candidates = titleMatches.length === 1
      ? titleMatches
      : distinctJsonCandidates;
    if (candidates.length === 1) {
      return {
        ok: true,
        source_text: candidates[0].source_text,
        structured: candidates[0].structured,
        extractor: { version: extractorVersion, strategy: "jobposting_jsonld" },
      };
    }
    return { ok: false, error_code: "CAPTURE_AMBIGUOUS" };
  }

  const body = renderedText(document.body, true);
  sawOversized = sawOversized || body.too_large;
  const specificSelectors = [
    '[itemprop="description"]',
    '[data-testid*="job-description" i]',
    '[data-test*="job-description" i]',
    '[aria-label*="job description" i]',
    '[id*="job-description" i]',
    '[class*="job-description" i]',
  ];
  const candidateByElement = new Map();
  const addCandidate = (
    element,
    origin,
    strategy,
    baseScore,
    requiredSignals,
  ) => {
    if (!(element instanceof Element) || !visibleElement(element)) {
      return;
    }
    const existing = candidateByElement.get(element);
    if (!existing || baseScore > existing.baseScore) {
      candidateByElement.set(element, {
        baseScore,
        element,
        origin,
        requiredSignals,
        strategy,
      });
    }
  };

  if (provider) {
    for (const element of queryVisible(provider.detailSelectors, 30)) {
      addCandidate(element, "provider", "provider_container", 18, 0);
    }
  }
  for (const element of queryVisible(specificSelectors, 40)) {
    addCandidate(element, "specific", "ranked_container", 14, 1);
  }

  const normalizedPageTitle = normalizedTitle(pageTitle);
  if (normalizedPageTitle) {
    const headingElements = queryVisible(
      ["h1", "h2", "h3", '[role="heading"]'],
      maximumTitleOccurrences,
    )
      .filter((element) => normalizedTitle(textForTitle(element)) === normalizedPageTitle);
    for (const heading of headingElements) {
      let ancestor = heading.parentElement;
      for (let depth = 0; ancestor && depth < 7; depth += 1) {
        if (ancestor === document.body || ancestor === document.documentElement) {
          break;
        }
        addCandidate(
          ancestor,
          "title",
          "ranked_container",
          Math.max(7, 12 - depth),
          1,
        );
        ancestor = ancestor.parentElement;
      }
    }
  }

  for (const element of queryVisible(['main', 'article', '[role="main"]'], 30)) {
    addCandidate(element, "semantic", "ranked_container", 5, 2);
  }
  addCandidate(document.body, "body", "guarded_body", 2, 2);

  const shellPhrases = [
    "search results",
    "jobs for you",
    "recommended jobs",
    "similar jobs",
    "people also viewed",
    "more jobs",
    "browse jobs",
    "filter results",
    "sort by",
    "job alerts",
  ];
  const sectionHeading = /^(about (?:the )?(?:job|role|position|team|company)|job description|description|responsibilities|requirements|required qualifications|preferred qualifications|qualifications|what (?:you will|you'll|we're looking for)|skills|experience|benefits|compensation|salary|equal opportunity|how to apply)$/i;
  const searchRegionSelectors = [
    '[role="listbox"]',
    '[aria-label*="job search results" i]',
    '[aria-label*="job results" i]',
    '[aria-label*="search results" i]',
    '[data-testid*="job-list" i]',
    '[class*="jobs-search-results" i]',
    '[class*="job-list" i]',
    '[id*="job-list" i]',
  ];
  const matchesOrContains = (element, selectors) => selectors.some((selector) => {
    try {
      return element.matches(selector) || Boolean(element.querySelector(selector));
    } catch {
      return false;
    }
  });
  const looksLikeJobLink = (anchor) => {
    const raw = anchor.getAttribute("href") || "";
    if (!raw || raw.startsWith("#")) {
      return false;
    }
    let parsed;
    try {
      parsed = new URL(raw, globalThis.location.href);
    } catch {
      return false;
    }
    return /(?:\/jobs?(?:\/|$)|\/viewjob(?:\/|$)|[?&](?:vjk|jk|currentjobid|jobpostingid|jobid)=)/i
      .test(`${parsed.pathname}${parsed.search}`);
  };
  const metricsFor = (candidate, result) => {
    const element = candidate.element;
    const anchors = queryVisibleWithin(element, ["a[href]"], 150);
    const jobLinks = anchors.filter(looksLikeJobLink);
    const linkedTitles = new Set(jobLinks
      .map(textForTitle)
      .filter((value) => (
        value.length >= 3
        && value.length <= 160
        && !/^(apply|view|details|learn more|save|open)$/i.test(value)
      ))
      .map(normalizedTitle)
      .filter(Boolean));
    const headings = queryVisibleWithin(element, ["h1", "h2", "h3", '[role="heading"]'], 100)
      .map(textForTitle)
      .filter(Boolean);
    const roleHeadings = new Set(headings
      .filter((value) => !sectionHeading.test(value) && !genericTitle.test(value))
      .map(normalizedTitle)
      .filter(Boolean));
    const folded = result.text.toLowerCase();
    const shellSignals = shellPhrases.filter((phrase) => folded.includes(phrase)).length;
    const searchRegion = matchesOrContains(element, searchRegionSelectors);
    const controls = queryVisibleWithin(
      element,
      ['button, input, select, textarea, [role="button"]'],
      50,
    ).length;
    const coverage = body.text && !body.too_large
      ? Math.min(1, result.text.length / body.text.length)
      : 0;
    return {
      controls,
      coverage,
      jobLinkCount: jobLinks.length,
      linkedTitleCount: linkedTitles.size,
      roleHeadingCount: roleHeadings.size,
      searchRegion,
      shellSignals,
    };
  };
  function queryVisibleWithin(root, selectors, limit = 100) {
    const found = [];
    for (const selector of selectors) {
      let matches;
      try {
        matches = root.querySelectorAll(selector);
      } catch {
        continue;
      }
      for (const element of matches) {
        if (found.length >= limit) {
          return found;
        }
        if (visibleElement(element) && !found.includes(element)) {
          found.push(element);
        }
      }
    }
    return found;
  }
  const contaminated = (candidate, metrics) => {
    if (metrics.linkedTitleCount >= 3 || metrics.jobLinkCount >= 5) {
      return true;
    }
    if (metrics.searchRegion && metrics.jobLinkCount >= 2) {
      return true;
    }
    if (metrics.shellSignals >= 2 && metrics.jobLinkCount >= 2) {
      return true;
    }
    if (
      metrics.roleHeadingCount >= 4
      && (metrics.searchRegion || metrics.jobLinkCount >= 2)
    ) {
      return true;
    }
    if (candidate.origin === "body") {
      return Boolean(
        !pageTitle
        || metrics.searchRegion
        || metrics.shellSignals > 0
        || metrics.jobLinkCount > 1
        || metrics.roleHeadingCount > 2
      );
    }
    return Boolean(
      ["semantic", "title"].includes(candidate.origin)
      && metrics.coverage >= 0.8
      && (
        metrics.searchRegion
        || metrics.shellSignals > 0
        || metrics.jobLinkCount > 1
      )
    );
  };
  const evaluated = [];
  const boundedCandidates = [...candidateByElement.values()]
    .sort((left, right) => right.baseScore - left.baseScore)
    .slice(0, maximumCandidateElements);
  for (const candidate of boundedCandidates) {
    const result = candidate.origin === "body"
      ? body
      : renderedText(candidate.element, true);
    sawOversized = sawOversized || result.too_large;
    if (!usable(result, candidate.requiredSignals)) {
      continue;
    }
    const metrics = metricsFor(candidate, result);
    if (contaminated(candidate, metrics)) {
      continue;
    }
    const titlePresent = normalizedPageTitle && result.text
      .split("\n")
      .slice(0, 60)
      .some((line) => normalizedTitle(line) === normalizedPageTitle);
    const lengthScore = result.text.length >= 600
      ? 4
      : result.text.length >= 300
        ? 3
        : 2;
    const score = candidate.baseScore
      + Math.min(4, jobSignalCount(result.text)) * 2
      + lengthScore
      + (titlePresent ? 3 : 0)
      - Math.min(6, metrics.jobLinkCount) * 2
      - metrics.shellSignals * 3
      - (metrics.searchRegion ? 5 : 0)
      - Math.max(0, metrics.roleHeadingCount - 2) * 2
      - (metrics.controls > 10 ? 3 : 0);
    if (score >= 12) {
      evaluated.push({ ...candidate, result, score });
    }
  }

  const byText = new Map();
  for (const candidate of evaluated) {
    const existing = byText.get(candidate.result.text);
    if (!existing || candidate.score > existing.score) {
      byText.set(candidate.result.text, candidate);
    }
  }
  const ranked = [...byText.values()].sort((left, right) => (
    right.score - left.score
    || left.result.text.length - right.result.text.length
  ));
  const winner = ranked[0];
  const runnerUp = ranked[1];
  const related = winner && runnerUp && (
    winner.element.contains(runnerUp.element)
    || runnerUp.element.contains(winner.element)
  );
  if (
    winner
    && (
      !runnerUp
      || winner.score - runnerUp.score >= 3
      || (related && winner.score >= runnerUp.score)
    )
  ) {
    return {
      ok: true,
      source_text: winner.result.text,
      structured: {
        ...emptyStructured(),
        title: pageTitle,
      },
      extractor: { version: extractorVersion, strategy: winner.strategy },
    };
  }
  if (sawOversized) {
    return { ok: false, error_code: "CAPTURE_TOO_LARGE" };
  }
  if (!body.text || body.text.length < minimumCharacters) {
    return { ok: false, error_code: "CAPTURE_EMPTY" };
  }
  return { ok: false, error_code: "CAPTURE_AMBIGUOUS" };
}
