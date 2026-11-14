package readcommon

// BundleBase reads the base commit a bundle was fetched at: the local
// source.json base_sha, else the PR record's baseRefOid, else its base_sha.
// store is the caller's decoded bundle so the run reads PR.json once.
func BundleBase(dir string, store *Bundle) string {
	if base := JSONStr(dir+"/source.json", "base_sha"); base != "" {
		return base
	}
	if store == nil {
		store = OpenBundle(dir)
	}
	if base := store.StrField("baseRefOid"); base != "" {
		return base
	}
	return store.StrField("base_sha")
}
