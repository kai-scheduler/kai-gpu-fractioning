# Governance

kai-gpu-fractioning is a repository of the [KAI Scheduler](https://github.com/kai-scheduler/KAI-Scheduler)
project, a [Cloud Native Computing Foundation](https://cncf.io) sandbox project. It is not a
separate project: the KAI Scheduler
[project governance](https://github.com/kai-scheduler/KAI-Scheduler/blob/main/GOVERNANCE.md)
applies here in full, and this document only records how it is applied to this repository.

Everything in this repository is licensed under the Apache License 2.0 (code) and the Creative
Commons Attribution 4.0 International License (documentation), and every inbound contribution
must carry a Developer Certificate of Origin sign-off, in line with the
[CNCF Charter](https://github.com/cncf/foundation/blob/main/charter.md) section 11.

## Roles

### Maintainers

Maintainers are responsible for the long-term health of this repository. They are listed in
[MAINTAINERS.md](MAINTAINERS.md), and the same list drives review and merge permissions through
[OWNERS](OWNERS) and [CODEOWNERS](CODEOWNERS).

Maintainers:

- set and steer the technical direction of the repository, in step with KAI Scheduler;
- review and approve pull requests, and are the only people who can merge them;
- own release management and the published artifacts;
- keep documentation, dependencies and security posture current;
- uphold the [Code of Conduct](CODE_OF_CONDUCT.md) and mediate disputes.

### Contributors

Anyone who opens an issue or a pull request. Contributors follow
[CONTRIBUTING.md](CONTRIBUTING.md), sign off every commit under the
[Developer Certificate of Origin](CLA.md), and are expected to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

### Community

Everyone else with an interest in the project — adopters, integrators and users. Feedback, bug
reports and adoption stories ([ADOPTERS.md](ADOPTERS.md)) are contributions too.

## Decision making

Decisions are made by lazy consensus among the maintainers, in public, on the pull request or
issue where the change is proposed.

- **Ordinary changes** — bug fixes, tests, documentation, dependency bumps: one maintainer
  approval plus green CI. Merging requires code-owner review; the branch protection ruleset on
  `main` enforces both.
- **Significant changes** — a new CRD field or API, a change in the operator's contract with the
  node daemons, a new component, a change to the security model, or anything that affects users
  of KAI Scheduler: opened as an issue first, and merged only once no maintainer objects.
- **Disagreement** — if consensus is not reached, any maintainer may call for a vote. A simple
  majority of maintainers decides; the maintainers of KAI Scheduler arbitrate anything that
  crosses repository boundaries.

## Changing the maintainer group

New maintainers are proposed by an existing maintainer, based on a sustained record of
contribution and review in this repository. The proposal is opened as a pull request against
[MAINTAINERS.md](MAINTAINERS.md), the community is invited to comment, and it is merged if no
existing maintainer objects within one week.

Maintainers who are inactive for six months, or who ask to step down, are moved out of
[MAINTAINERS.md](MAINTAINERS.md) by the same process. Stepping down is normal and carries no
stigma; former maintainers are welcome back on the same terms as anyone else.

The maintainer group is not required to remain single-vendor and is open to contributors from any
organization, consistent with CNCF's vendor-neutrality expectations.

## Code of Conduct

This repository follows the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/main/code-of-conduct.md),
as adopted in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Violations are reported to the
maintainers, or to the CNCF Code of Conduct Committee at <conduct@cncf.io>, and are reviewed
privately.

## Security

Vulnerabilities are reported privately through the channels in [SECURITY.md](SECURITY.md), never
as a public issue.

## Changing this document

By pull request, approved by a majority of the maintainers.
