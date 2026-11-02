package main

// Shared argv literals: the front-door parser and the verb adapters use the
// same spellings, so one constant keeps the grammar and the re-entry hints in
// sync across cmd/verdict files.
const (
	cmdShow        = "show"
	cmdInstallVerb = "install"
	flagJSON       = "--json"
	flagHelp       = "--help"
	flagShortHelp  = "-h"
	flagDryRun     = "--dry-run"
	formatJSONName = "json"
	entryDryRun    = " " + flagDryRun
	entryJSON      = " " + flagJSON
)
