# Notices

## Adapted review prompts

The review prompts under `skills/verdict/prompts/` are adapted from
[Pullfrog](https://github.com/pullfrog/pullfrog), an MIT-licensed open-source GitHub Action. The
skill's review discipline additionally borrows from clawpatch and pstack, as described in
`docs/vision.md`. All lineage is credited, not hidden.

- Upstream repository: <https://github.com/pullfrog/pullfrog>
- Pinned upstream commit: `bef4f79cef8c9793608ef50179bae9e925231e38`

The skill directory ships its own copy of this notice, `skills/verdict/NOTICE.md`, because
`npx skills add` copies that directory alone: the adapted prompts have to carry the upstream
license with them, not leave it behind in this repository-root file. The two copies hold the same
license block, and `TestNoticeCoversAdaptedPaths` fails when they drift or when a prompt is
missing from the payload copy.

<!-- upstream-license:start -->

MIT License

Copyright (c) 2026 Pullfrog, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
<!-- upstream-license:end -->

## Doctrine credits, no text reproduced

clawpatch and pstack informed the review discipline in prose. No text from either project is
reproduced in this repository or in the shipped payload, so neither one raises a notice
requirement here.
