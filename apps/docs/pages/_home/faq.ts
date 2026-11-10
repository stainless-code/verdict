/**
 * Homepage FAQ. Answers use `code` spans written with backticks; Faq.astro
 * renders them and index.astro strips the backticks for the FAQPage JSON-LD.
 * Every answer is checked against docs/manual.md and the binary's own `--help`;
 * the mapping is in docs/landing-claims.md.
 *
 * Four questions survive. Cost moved to the pricing band, and "is my code sent
 * anywhere" moved to the trust table, so neither is answered twice.
 */
export interface Qa {
  q: string;
  a: string[];
}

export const faq: Qa[] = [
  {
    q: "Does it post without me?",
    a: [
      "No. Nothing runs in the background. A review posts when you run `verdict post`, or when an agent you gave that command runs it.",
      "Try `verdict post --dry-run` first. It validates the anchors and prints the payload without posting. Approving or requesting changes needs `--bind`, and an approval is refused while findings are open unless you pass `--force`. `verdict mcp --read-only` gives an agent a server that cannot write.",
    ],
  },
  {
    q: "Which agents does it work with?",
    a: [
      "Any agent that can read files and run commands. `verdict skill install --agent` takes claude, cursor, codex, opencode, copilot, gemini, goose, qwen, windsurf or agents, and `--detect` installs into every one whose skills directory exists and is non-empty.",
      "Verdict pins no model. The agent you run is the review model.",
    ],
  },
  {
    q: "Which hosts does it work with?",
    a: [
      "`fetch` reads GitHub, GitLab, GitCafe, Forgejo, Bitbucket and Azure DevOps. On all six, `record`, `report`, `triage`, `show` and `next` work on the fetched bundle.",
      "`post` submits to GitHub, GitLab, and Forgejo: GitLab takes the approval but has no request-changes event, so a bound request-changes posts as a comment and `post.json` records `withheld[]`.",
      "`adopt` reaches any host verdict can post to, while `ready` and the live `calibrate` join run on GitHub and GitLab. Elsewhere they refuse with a typed error; `calibrate --probe` is offline and works on any bundle.",
    ],
  },
  {
    q: "Can it review something other than a PR?",
    a: [
      "Yes. `fetch-local owner/repo work` builds a review bundle from git alone: a branch against a base (`--base main`), a commit range (`--range A..B`), staged changes (`--staged`), a stash entry (`--stash [n]`), or an isolated checkout (`--worktree`).",
      "`record`, `report`, `triage`, `show` and `next` all run on the local bundle. Only `post` needs a PR target, and it refuses a local bundle with `no-pr-target` instead of inventing one.",
    ],
  },
];
