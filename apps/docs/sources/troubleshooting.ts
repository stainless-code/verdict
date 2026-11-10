import type { ContentSource } from "blume/sources";

import { docsPage } from "../docs-index.ts";
import { stagedDocSource } from "./staged-doc.ts";

export const troubleshootingSource = (): ContentSource =>
  stagedDocSource(docsPage("troubleshooting.md"), "01-docs/(reference)");
