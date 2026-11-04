/**
 * One source for the landing page's install command. The hero and the closing
 * band must never show different routes: the closing band used to mount a
 * second copy of the full installer and drift from the hero.
 *
 * The closing band mirrors whatever the hero's switcher shows; without
 * JavaScript it shows the same route the hero starts on.
 */
export const CLI_INSTALL = "npm i -g @stainless-code/verdict";

/**
 * Split a command into segments that may break at their end, so a wrapped
 * route breaks after a path separator instead of inside a word. Breaks are
 * offered only after a lone `/` or after a scheme's `//`: never after `-`,
 * which would split a hyphenated word (`@stainless-` / `code`). `<wbr>` adds
 * no text, so a copy handler still reads the whole command from `textContent`.
 */
export const breakable = (command: string): string[] => {
  const parts: string[] = [];
  let start = 0;
  let i = 0;
  while (i < command.length) {
    if (command[i] === "/") {
      const next = command[i + 1] ?? "";
      const prev = command[i - 1] ?? "";
      if (next === "/" && prev === ":") {
        i += 2;
        parts.push(command.slice(start, i));
        start = i;
        continue;
      }
      if (prev !== "/" && next !== "" && next !== "/") {
        parts.push(command.slice(start, i + 1));
        start = i + 1;
      }
    }
    i += 1;
  }
  parts.push(command.slice(start));
  return parts;
};
