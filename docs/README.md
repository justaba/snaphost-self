# Documentation

Status: Current
Updated: 2026-08-30

This directory describes snaphost-self after the single-binary collapse. The
current code and Compose manifests are the final authority; a current document
that disagrees with them is a documentation defect.

## Understand the system

- [Architecture overview](architecture/overview.md)
- [Packages and external components](architecture/services.md)
- [Deploy lifecycle](architecture/deploy-lifecycle.md)
- [Projects, deploys and routing](architecture/deployment-model.md)
- [Security model](architecture/security.md)

## Develop and operate it

- [Local development](operations/local-development.md)
- [Install and upgrade](operations/install-and-upgrade.md)
- [CI and deployment](operations/ci-cd.md)
- [Custom domains](operations/custom-domains.md)
- [Backup and restore](operations/backups.md)
- [Monitoring](operations/monitoring.md)
- [Rollback](operations/rollback.md)
- [Troubleshooting](operations/troubleshooting.md)

The versioned installation contract is implemented in `infra/snaphostctl` and
documented in the install runbook. It still needs the real-host acceptance
drills tracked by [Task 7](tasks/planned/0007-install-and-upgrade.md), and it
does not make the incomplete production routing edge disappear.

## Decisions and work status

- [Architecture decision records](decisions/README.md)
- [Task catalog](tasks/README.md)
- [Completed Task 1](tasks/completed/0001-collapse-to-one-binary.md)

## Document contract

- architecture/ describes behavior that exists now;
- operations/ contains procedures for the current tree and explicitly labels
  any environment-specific or unproven path;
- decisions/ records choices and whether they remain accepted or were
  superseded;
- tasks/ records current and planned work;

Root README.md is the operator entry point. CLAUDE.md is the detailed
repository guide for coding agents. Neither overrides code or configuration.
