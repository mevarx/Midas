#!/usr/bin/env python3
"""Release qualification gate.

Enforces the standing qualification checklist from docs/RELEASE.md that must
hold for every release tag. The release workflow runs this before GoReleaser
publication; it fails with the list of unmet gates so a tag cannot publish a
release that lost its qualification.

Checks are the machine-verifiable part of the checklist: required lifecycle
test coverage (move/restore/rollback with failure injection and interruption
recovery, backup, platform, prior-version metadata compatibility), required
policy and recovery documentation, and every qualification checkbox in
docs/RELEASE.md being confirmed. Everything the checklist attestates that a
script cannot prove (native CI results, reviewed release notes) is re-verified
by the surrounding release workflow and the draft-review step.
"""

from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

failures: list[str] = []
confirmed: list[str] = []


def gate(condition: bool, unmet: str, met: str | None = None) -> bool:
    if condition:
        if met:
            confirmed.append(met)
    else:
        failures.append(unmet)
    return condition


def repo(rel: str) -> pathlib.Path:
    return ROOT / rel


def has_test_functions(path: pathlib.Path, minimum: int) -> bool:
    if not path.is_file():
        return False
    return len(re.findall(r"^func Test", path.read_text(encoding="utf-8"), re.M)) >= minimum


# Lifecycle test coverage: dedicated end-to-end tests for the central
# migration lifecycle, including failure injection and interruption recovery.
for name in ("move_test.go", "restore_test.go", "rollback_test.go"):
    path = repo(f"internal/mover/{name}")
    gate(
        has_test_functions(path, 1),
        f"missing migration lifecycle coverage: internal/mover/{name} does not exist or has no tests",
        f"lifecycle coverage present: internal/mover/{name}",
    )

lifecycle_sources = "".join(
    repo(f"internal/mover/{name}").read_text(encoding="utf-8") if repo(f"internal/mover/{name}").is_file() else ""
    for name in ("move_test.go", "restore_test.go", "rollback_test.go")
)
gate(
    "TestMove" in lifecycle_sources and "TestRestore" in lifecycle_sources and "TestRollback" in lifecycle_sources,
    "missing lifecycle tests: move, restore, and rollback must each have named end-to-end tests",
)
gate(
    re.search(r"Interruption|Canceled|canceled|interrupted", lifecycle_sources) is not None,
    "missing interruption-recovery coverage: lifecycle tests must cover cancellation and retry",
)
gate(
    re.search(r"Refuses|corrupt|Corrupt|tamper", lifecycle_sources) is not None,
    "missing failure-injection coverage: lifecycle tests must cover refusal and corruption paths",
)

# Backup protection and recovery coverage.
gate(
    has_test_functions(repo("internal/backup/set_test.go"), 3),
    "missing backup protection coverage: internal/backup/set_test.go needs at least 3 tests",
    "backup protection coverage present: internal/backup/set_test.go",
)
gate(
    has_test_functions(repo("internal/platform/platform_test.go"), 1),
    "missing platform coverage: internal/platform/platform_test.go needs tests",
    "platform coverage present: internal/platform/platform_test.go",
)

# Prior-version metadata compatibility is tested.
legacy_test = repo("internal/config/config_test.go")
legacy_ok = legacy_test.is_file() and "TestLoadLegacyConfigDefaultsSchemaVersion" in legacy_test.read_text(encoding="utf-8")
gate(
    legacy_ok,
    "missing prior-version metadata compatibility test: TestLoadLegacyConfigDefaultsSchemaVersion",
    "prior-version metadata compatibility tested: TestLoadLegacyConfigDefaultsSchemaVersion",
)

# Policy, safety, and recovery documentation.
required_docs = (
    "COMPATIBILITY.md",
    "INSTALL.md",
    "SAFETY.md",
    "RECOVERY.md",
    "SECURITY_REVIEW.md",
    "PLATFORMS.md",
    "DECISIONS.md",
)
for name in required_docs:
    path = repo(f"docs/{name}")
    gate(
        path.is_file() and path.stat().st_size > 0,
        f"missing required documentation: docs/{name}",
        f"documentation present: docs/{name}",
    )

decisions = repo("docs/DECISIONS.md")
gate(
    decisions.is_file() and "Known limitations" in decisions.read_text(encoding="utf-8"),
    "platform limitations are not documented: docs/DECISIONS.md needs a 'Known limitations' section",
    "known platform limitations documented in docs/DECISIONS.md",
)

security = repo("docs/SECURITY_REVIEW.md")
gate(
    security.is_file() and re.search(r"disposition|Finding", security.read_text(encoding="utf-8")) is not None,
    "security review findings are not dispositioned: docs/SECURITY_REVIEW.md",
    "security-review findings dispositioned in docs/SECURITY_REVIEW.md",
)

# The qualification checklist in docs/RELEASE.md must be fully confirmed.
release_doc = repo("docs/RELEASE.md")
gate(release_doc.is_file(), "missing docs/RELEASE.md")
if release_doc.is_file():
    text = release_doc.read_text(encoding="utf-8")
    start = text.find("Before tagging")
    gate(start >= 0, "docs/RELEASE.md has no qualification checklist ('Before tagging' section)")
    if start >= 0:
        section = text[start:]
        end = re.search(r"\n#{1,3} ", section[1:])
        section = section[: end.start() + 1] if end else section
        boxes = re.findall(r"^-\s+\[( |x)\]", section, re.M)
        unchecked = [line.strip() for line in section.splitlines() if re.match(r"^-\s+\[ \]", line.strip())]
        gate(len(boxes) >= 5, "qualification checklist has fewer than 5 items; docs/RELEASE.md checklist was weakened")
        gate(
            not unchecked,
            "unconfirmed release qualification items:\n  " + "\n  ".join(unchecked),
        )
        gate(bool(boxes), "qualification checklist has no confirmed items")
        if boxes and not unchecked:
            confirmed.append(f"qualification checklist fully confirmed ({len(boxes)} items)")

if failures:
    print("RELEASE GATE FAILED:", file=sys.stderr)
    for failure in failures:
        print(f"  - {failure}", file=sys.stderr)
    sys.exit(1)

print("release gate passed:")
for item in confirmed:
    print(f"  - {item}")
