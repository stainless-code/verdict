package verifycore

// DetachOptionValues exposes the splitter to the external fuzz test.
var DetachOptionValues = detachOptionValues

// FilterChildEnv exposes the child environment filter to external table tests.
var FilterChildEnv = filterChildEnv

// SetAccountHomeForTest pins the account home the sandbox hides credentials
// under, so an exact-argv expectation does not depend on the host's own
// passwd entry.
func SetAccountHomeForTest(req *Request, home string) {
	req.accountHome = home
}
