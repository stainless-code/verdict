/**
 * Brand marks for the install switcher, from Simple Icons (https://simpleicons.org,
 * CC0 1.0). Each mark is a real `.svg` file under `icons/`, imported as an
 * Astro component: since Astro 5.7 an SVG default import is a component whose
 * content is inlined into the HTML at build time, and props such as `class`
 * and `fill` override the file's own. A separate `<img src="….svg">` would cost
 * a request per icon and could not follow `currentColor` (an `<img>` is a
 * closed document), so inlining is the right shape for a themed icon strip.
 *
 * Source versions: simple-icons 16.36.0 for npm, pnpm, Bun, Yarn, Homebrew,
 * Apple and Linux (Tux). Windows comes from simple-icons 11.15.0, the last
 * release that ships it; later releases dropped the Microsoft marks. Paths in
 * `icons/` are the 24x24 outlines those releases publish, with `fill` set to
 * `currentColor` so the switcher follows the theme.
 *
 * Brand names and logos belong to their owners. They identify the install route
 * and imply no endorsement.
 */
import type { SvgComponent } from "astro/types";
import apple from "./icons/apple.svg";
import bun from "./icons/bun.svg";
import homebrew from "./icons/homebrew.svg";
import linux from "./icons/linux.svg";
import npm from "./icons/npm.svg";
import pnpm from "./icons/pnpm.svg";
import windows from "./icons/windows.svg";
import yarn from "./icons/yarn.svg";

export const brandIcons = {
  npm,
  pnpm,
  bun,
  yarn,
  homebrew,
  apple,
  linux,
  windows,
} satisfies Record<string, SvgComponent>;

export type BrandIcon = keyof typeof brandIcons;
