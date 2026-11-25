---
"@stainless-code/verdict": minor
---

`verdict init --ci gitlab` writes `.gitlab/ci/verdict-review.yml`, a one-job merge-request pipeline that installs verdict, fetches with `--provider gitlab`, checks readiness, reviews with your agent, posts, reports and gates. With no root pipeline it also writes a minimal `.gitlab-ci.yml`; an existing one is never modified, even with `--force`, and the include line to add is printed instead. `--print` renders both files without touching the tree.
