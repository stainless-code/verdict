/**
 * Homepage FAQ. Answers use `code` spans written with backticks; Faq.astro
 * renders them and index.astro strips the backticks for the FAQPage JSON-LD.
 * Every answer is checked against docs/manual.md and the binary's own `--help`;
 * the sources are listed in .scratch/onboarding/O-landing.md.
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
      "Try `verdict post --dry-run` first: it validates the anchors and prints the payload without posting. Approving or requesting changes needs `--bind`, and an approval is refused while findings are open unless you pass `--force`. `verdict mcp --read-only` gives an agent a server that cannot write.",
    ],
  },
  {
    q: "What does it cost?",
    a: [
      "Verdict is MIT licensed, with no subscription, seats or per-review meter. Your agent's model usage is billed by your model provider at its own rates.",
      "To cap that bill, set `REVIEW_BUDGET_USD`. Fetch then estimates the run from your past runs and refuses one projected over budget, unless you pass `--consent-over-budget`.",
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
      "Posting, `adopt`, `ready`, `calibrate` and incremental re-review need a GitHub bundle. On the other hosts they refuse with a typed error.",
    ],
  },
  {
    q: "Is my code sent anywhere?",
    a: [
      "Verdict has no server and no telemetry, and it never updates itself in the background. It reads your host with your credentials and posts only when `verdict post` runs.",
      "Your agent sends the diff to whichever model provider it uses. That is your choice, not Verdict's, and provider tokens go only to their own hosts.",
    ],
  },
];
