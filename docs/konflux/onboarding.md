# Konflux onboarding guide

Team reference for onboarding HyperFleet container images to Konflux and keeping new builds on the Konflux path.

**Tenant:** `rosa-tenant` on `kflux-prd-rh02`  
**Konflux UI:** [rosa-tenant applications](https://konflux-ui.apps.kflux-prd-rh02.0fk9.p1.openshiftapps.com/ns/rosa-tenant/applications)

## Goals

- **Konflux builds** every merge to `main` and every pull request (supply chain + attestation).
- **Prow** keeps lint, unit, integration, and e2e orchestration (Hypershift-style hybrid — do not remove Prow image jobs until deliberately cut over).
- **GitOps** (`rosa-hyperfleet` Helm values) pins `quay.io/redhat-user-workloads/rosa-tenant/<image>:<sha>`.

See also: [Quay image tags and pinning](./quay-image-tags.md).

## Onboarding checklist (per component)

Copy this checklist for each new image. **Onboarded** means steps 1–5 are complete: green PR build before merge, green `main` push build after merge, and GitOps pointed at the Konflux image.

| # | Step | Where | Done when |
|---|------|--------|-----------|
| 1 | Register Application + Component + ImageRepository | [`konflux-release-data`](https://gitlab.cee.redhat.com/releng/konflux-release-data) overlay under `tenants-config/cluster/kflux-prd-rh02/tenants/rosa-tenant/overlay/<app>/main/` | MR merged; Component visible in Konflux UI |
| 2 | Add build pipelines | App repo `.tekton/*-on-pull-request.yaml` + `*-on-push.yaml` | PR merged (Konflux bootstrap PR or hand-copied from a reference repo) |
| 3 | Wire CI gates | [`openshift/release`](https://github.com/openshift/release) branch protection + ci-operator if needed | Konflux on-PR check required on `main`; use `skip-unknown-contexts: true` during rollout |
| 4 | Validate builds | Konflux UI / GitHub checks | Green `*-on-pull-request` on a test PR and green `*-on-push` on `main` |
| 5 | Point consumers at Konflux image | `rosa-hyperfleet` ArgoCD values (or other deploy repo) | `repository` + `tag: "<full-sha>"` under `redhat-user-workloads/rosa-tenant/…` |

### Reference implementation

Use **this repository** as the template for step 2:

- `platform-api` — `.tekton/rosa-hyperfleet-api-*.yaml`
- `hyperfleet-operator` — `.tekton/rosa-hyperfleet-operator-*.yaml`

For step 1, copy an existing overlay (for example `rosa-boundary` or `rosa-hyperfleet-api` under `rosa-tenant`) and adjust:

- `application-patch.yaml` — Application name
- `component-patch.yaml` — Component name, git URL, branch, `spec.build-nudges-ref` if needed
- `image-repository.yaml` — Quay path under `redhat-user-workloads/rosa-tenant/`

Run `build-manifests.sh` in `konflux-release-data/tenants-config` before opening the MR.

### New repository vs new component

| Case | Application | Component | Repo change |
|------|-------------|-----------|-------------|
| New image in an existing repo (like operator in this repo) | Reuse `rosa-hyperfleet` | New component name | Add `.tekton/<component>-*.yaml` |
| New standalone repo (like `rosa-hyperfleet-zoa`) | New or existing application | New component | Full `.tekton/` + release config |

## Current onboarding status

HyperFleet images and where they stand on the checklist above.

| Component | Repository | konflux-release-data | `.tekton/` | openshift/release | Validated build |
|-----------|------------|----------------------|------------|-------------------|-----------------|
| `platform-api` | `rosa-hyperfleet-api` | Done | Done | Done | Done |
| `hyperfleet-operator` | `rosa-hyperfleet-api` | Done | Done | Done | Done |
| `aws-nuke-cf` | `aws-nuke-cf` | Done | Done | Done | Done |
| `rosa-hyperfleet-zoa` | `rosa-hyperfleet-zoa` | Done | Done | Branch protection PR open | In progress |
| `rosa-hyperfleet-kube-applier` | `rosa-hyperfleet-kube-applier` | Done | Done | Done | In progress |
| `zoa-tools` | `rosa-hyperfleet` (`images/zoa-tools/`) | Not started | Not started | — | — |
| RRP CI runner | `rosa-hyperfleet` (`ci/Containerfile`) | Not started | Not started | — | — |
| `rosa-hyperfleet-cli` | `rosa-hyperfleet-cli` | Not started | Not started | — | Deferred (lower priority) |

**Rule for new work:** any new container image that ships to staging or production must complete steps 1–3 before merge and steps 4–5 before release. Do not use ad-hoc personal Quay repos for runtime images.

## Prow and Konflux responsibilities

| Concern | Owner |
|---------|--------|
| Image build + attestation | Konflux |
| `gomod` / Dockerfile / Tekton dep bumps | MintMaker (`renovate.json` in each onboarded repo) |
| Lint, verify, unit, integration, e2e | Prow |
| Required checks before merge | Both (Konflux on-PR + Prow jobs) |

Konflux can only attest images **it** built. Released images must come from Konflux push pipelines, not from ci-operator `images:` alone.

## Dependency PRs (MintMaker)

This repo uses root [`renovate.json`](../../renovate.json) for MintMaker:

- **gomod**, **dockerfile**, **tekton** managers enabled
- Patch/minor/digest updates: automerge when Prow + Konflux pass
- Major updates: manual review (`major-update`, `manual-review-required`)

MintMaker runs on a **~4 hour base schedule**. The `tekton` manager runs on Saturdays after 05:00 UTC. To rerun CI on an open dep PR without waiting:

- Comment **`/retest`** on the PR (Konflux + Prow)
- Add the **`rebase`** label (or use the rebase checkbox in the Renovate PR body) to refresh the branch against `main`

Automerge runs on a **subsequent** MintMaker pass after all checks are green and the branch is up to date with `main`.

## Ephemeral / PR testing

PR builds produce:

```text
quay.io/redhat-user-workloads/rosa-tenant/<image>:on-pr-<full-commit-sha>
```

Tags expire after **5 days**. To test a platform-api change in an ephemeral RC:

1. Open PR → wait for green `*-on-pull-request`
2. Pin ephemeral config to `:on-pr-<sha>` (see `rosa-hyperfleet` development docs)
3. Run ephemeral resync

Post-merge pins use the plain `<sha>` tag — see [quay-image-tags.md](./quay-image-tags.md).

## Troubleshooting

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| No Konflux check on PR | PAC not configured or GitHub App not on repo | Set `build.appstudio.openshift.io/request: configure-pac` on the Component; confirm app install |
| `on-pr-*` build fails, `main` is fine | Stale Tekton task refs | Merge Konflux/MintMaker Tekton bump PRs; `/retest` open dep PRs |
| EC (Enterprise Contract) failures | Dockerfile or base image policy | Check Konflux UI pipeline log; align with EC policy |
| Image not pullable from RC/EKS | ImageRepository visibility / pull secret | Konflux admin or registry credentials |
| MintMaker PR stuck | Real dep breakage vs infra | Read `renovate/artifacts` and Prow logs; close bad PRs |

## External references

- [Konflux: onboarding from GitHub](https://konflux-ci.dev/docs/building/creating-github/)
- [Konflux: running / retriggering pipelines](https://konflux-ci.dev/docs/building/running/) (`/retest`, `/ok-to-test`)
- [MintMaker user guide](https://konflux-ci.dev/docs/mintmaker/user/)
- [Create tenant namespace](https://konflux.pages.redhat.com/docs/users/getting-started/create-tenant-namespace.html) (HyperFleet reuses `rosa-tenant` — new tenants rarely needed)

## Related Jira

- ROSAENG-59370 — Konflux onboarding epic
- ROSAENG-59371 — `platform-api` Konflux pipeline (done)
- ROSAENG-60377 — Prow / Konflux interaction (done)
