package core

// SettingsTopKeys lists every top-level key the operator settings file
// accepts. The prefs loader, the typed personal parser, the unknown-key
// message, and the bad-settings hint all render from this one list.
func SettingsTopKeys() []string {
	return []string{
		"$schema", "output", "layout", "color", "publish",
		"review", "spend", "privacy", "packs", "agents",
	}
}
