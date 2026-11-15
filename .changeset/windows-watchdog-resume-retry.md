---
"@stainless-code/verdict": patch
---

On Windows, a watchdog-run command could fail before starting with `resume
suspended verify process: no thread resumed` under load. The thread lookup is
now retried a few times before the spawn fails, and a child whose thread never
appears is still killed with its job.
