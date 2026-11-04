package core

// Presence is one adapter-owned Has* bit: the bool that distinguishes an
// absent optional flag from an explicitly empty value. The CLI sets these
// while parsing argv, where the flag's presence is known; transports that
// only see decoded values derive them from the table each flag type owns.
type Presence struct { //nolint:govet // the row reads name/value/flag; alignment is irrelevant
	Name  string
	Value string
	Flag  *bool
}

// PresenceDeriver is implemented by flag structs that carry derived Has*
// bits. The table is the one place a new optional flag declares its presence
// rule, so no transport needs a per-field edit.
type PresenceDeriver interface {
	Presences() []Presence
}

// DerivePresences applies one flag type's presence table in place.
func DerivePresences(p PresenceDeriver) {
	for _, entry := range p.Presences() {
		*entry.Flag = entry.Value != ""
	}
}
