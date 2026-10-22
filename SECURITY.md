# Security Policy

## Supported Versions

Career Hub is currently in active pre-release development. Security fixes
target the latest revision of the default branch. Older commits, forks, and
unofficial builds are not supported unless the issue is also reproducible on
the current default branch.

## Reporting a Vulnerability

Please use **Report a vulnerability** in the repository's **Security** tab.

Do not disclose a suspected vulnerability in a public issue, discussion, pull
request, or social-media post.

A useful report includes:

- the affected revision and operating environment;
- clear reproduction steps using synthetic data;
- the expected and observed behavior;
- the practical security impact and required preconditions; and
- a minimal proof of concept when one is safe to share.

Do not include real resumes, personal data, API keys, access tokens, private
file paths, or other sensitive information. Replace sensitive values with
synthetic examples before submitting the report.

The maintainer aims to acknowledge complete reports within five business days.
Reports will be investigated privately, and public disclosure will be
coordinated after a fix or reasonable mitigation is available.

This project does not currently operate a paid bug-bounty program.

## Scope

Reports are in scope when they affect the current Career Hub code and cross a
meaningful security or privacy boundary, including:

- unauthorized exposure of local user data or credentials;
- unintended access to files outside the application's intended storage;
- exploitable processing of untrusted input; or
- compromise of the dependency, build, or release process.

The following are generally out of scope:

- product suggestions and non-security correctness issues;
- unsupported historical revisions and third-party forks;
- findings that require prior unrestricted control of the same operating-system
  account without crossing another security boundary; and
- automated scanner output without reproducible, reachable security impact.

Please test only against systems and data you own or are authorized to use.
