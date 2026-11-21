---
"@stainless-code/verdict": minor
---

`verdict init --ci gitlab` writes `.gitlab/ci/verdict-review.yml`, a one-job merge-request pipeline that installs verdict, fetches the bundle with `--provider gitlab`, pre-checks readiness, reviews with your agent, posts, reports, and gates. When the project has no root pipeline it also writes a minimal `.gitlab-ci.yml`; an existing root is never modified, with or without `--force`, and the exact include to add is printed instead. `--print` renders both files and the include without touching the tree, and `--print --json` reports `root_path`, `root_status`, `root_workflow`, and `include_snippet`.
