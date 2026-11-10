import type { ContentSource } from "blume/sources";

import { docsPage } from "../docs-index.ts";
import { stagedDocSource } from "./staged-doc.ts";

export const manualSource = (): ContentSource =>
  stagedDocSource(docsPage("manual.md"));
