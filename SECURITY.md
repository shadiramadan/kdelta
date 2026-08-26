# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities privately through
[GitHub Security Advisories](https://github.com/shadiramadan/kdelta/security/advisories/new).

**Do not open a public issue for security vulnerabilities.**

Include as much of the following as you can:

- A description of the vulnerability and its impact.
- Steps to reproduce, or a proof of concept.
- Affected versions or commits.

You should receive an acknowledgement within a few days. Please allow time for a
fix to be prepared and released before any public disclosure.

## Supported versions

kdelta is pre-1.0. Only the latest release receives security fixes.

## Supply chain

Release artifacts are built in GitHub Actions with SBOMs (syft), signatures
(cosign), and build provenance attestations. Verify images against the
attestations published alongside each release before deploying them.
