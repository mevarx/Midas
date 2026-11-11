# Security policy

Midas moves personal application data and its backups. Treat vulnerabilities
that could overwrite, expose, misclassify, or delete data as high impact.

## Reporting

Please report vulnerabilities privately through GitHub's **Report a
vulnerability** action for this repository (Security Advisories). Do not open a
public issue for a data-loss, path-traversal, backup-disclosure, or destructive
cleanup vulnerability. If private reporting is unavailable, contact a
repository maintainer privately before publishing details. Do not include
credentials, personal file contents, or backup archives in a report.

Include affected version/commit, platform/filesystem, a concise impact
description, safe reproduction steps using disposable temporary data, and any
known mitigation. Maintainers will acknowledge reports and coordinate a fix
and disclosure timeline; no response-time guarantee is made before v1.0.

## Scope

Security review includes path containment, symlink/junction handling, guarded
cleanup, backup protections, metadata tampering, permissions, temporary files,
child-process execution, app-definition validation, diagnostics, dependencies,
and release artifact integrity. Midas does not execute commands supplied by app
definitions, send telemetry, or upload user data.
