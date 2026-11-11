---
"@stainless-code/verdict": patch
---

On Windows, a command that failed to start — never attached to its job object,
or left with no thread to resume — and then survived verdict's cleanup kill
left the run waiting on it forever. That spawn failure now returns at once,
naming both the spawn error and the kill error, instead of hanging.
