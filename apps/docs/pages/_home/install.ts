/**
 * One source for the landing page's install command. The hero and the closing
 * band must never show different routes: the closing band used to mount a
 * second copy of the full installer and drift from the hero.
 *
 * The closing band mirrors whatever the hero's switcher shows; without
 * JavaScript it shows the same route the hero starts on.
 */
export const CLI_INSTALL = "npm i -g @stainless-code/verdict";
