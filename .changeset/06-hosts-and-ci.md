---
"@stainless-code/verdict": minor
---

**Hosts and CI**

- **Forgejo publish** — `post` submits one atomic Forgejo review with the body, verdict, reviewed commit, and every inline finding, and withholds what the host cannot anchor rather than guessing. [Submit per host](docs/manual.md#submit-post-per-host) <!-- changesets: forgejo-post.md -->
- **GitLab publish** — each finding becomes one positioned discussion, the review body a summary note, and an approval verdict a real MR approval. [Submit per host](docs/manual.md#submit-post-per-host) <!-- changesets: gitlab-publish.md -->
- **GitLab incremental** — `fetch --provider gitlab --base-sha` writes the same `incremental.diff` contract the GitHub compare path writes, and hosts without a compare adapter say so in a note. [Providers](docs/manual.md#providers-non-github-hosts) <!-- changesets: gitlab-incremental.md -->
- **GitLab readiness and calibration** — `ready` advises on merge requests and plain `calibrate` joins live GitLab threads through `glab`, on the same blocker and outcome vocabulary as GitHub. [Calibration](docs/manual.md#calibration-predictions-vs-ground-truth) <!-- changesets: ready-calibrate-gitlab.md -->
- **`doctor --provider`** — preflight one host at a time, with a per-host probe table and credentials reported by presence, never by value. [Doctor preflight](docs/manual.md#doctor-preflight) <!-- changesets: doctor-provider-adopt-gitlab.md -->
- **GitHub Action** — the review and merge gate ships as a first-party composite action that installs a pinned verified release and uses your runner, your agent, and your keys. [The runner is your CI](docs/manual.md#the-runner-is-your-ci) <!-- changesets: github-action.md -->
