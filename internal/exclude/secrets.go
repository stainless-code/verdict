package exclude

import (
	"path"
	"slices"
	"strings"
)

// SecretID and SecretReason label every built-in secret exclusion. Unlike
// default:generated, this set is not a policy default: it is the withholding
// floor, applied before and above every user rule, so no `.verdict/ignore`
// negation, no `--ignore`, and no `--no-default-excludes` can put a private
// key, credential file, or environment document back into the review diff.
const (
	SecretID     = "default:secret"
	SecretReason = "secret-builtin"
)

// secretEnvPatterns hold the environment-file spellings at the repository root
// and at any depth. `*` crosses slashes, so the nested forms cover every
// directory the diff can name.
var secretEnvPatterns = []string{".env", "*/.env", ".env.*", "*/.env.*"}

// secretEnvTemplates are the `.env` spellings that ship as documentation: no
// values, only key names. They stay reviewable, and the exemption is an
// explicit negation in the built-in list so a template is never excluded by
// accident. The list matches the sensitive-path manifest's exemption
// vocabulary (`sensEnvExempt`), so one answer covers both surfaces.
//
// Each exempted pattern is the basename itself, never a `*`-prefixed form: a
// `*.env.example` glob would also clear `prod.tfstate.env.example`, a
// Terraform state document whose name merely ends in a template spelling.
var secretEnvTemplates = []string{".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults"}

// secretNames are the exact secret-adjacent basenames excluded at any depth.
// `id_rsa.pub` and friends are absent on purpose: a public key is not a
// secret. The dotfile names and `id_*` spellings are the ones the
// sensitive-path manifest also reads (`sensNameSet`), so the advisory scan and
// the withholding floor agree on what a credential looks like; the credential
// documents it treats as extensions (`credentials.json`, `kubeconfig`) need
// their exact names here.
var secretNames = []string{
	"credentials.json", "kubeconfig", ".envrc", ".netrc", ".npmrc", ".pypirc",
	".pgpass", ".htpasswd", "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa",
}

// secretExts are the secret-adjacent file extensions, matched at any depth.
var secretExts = []string{"pem", "key", "p12", "pfx", "jks", "ppk", "keystore"}

// secretRules is the built-in set in match order: exclusions first, then the
// template negations a last-match-wins walk re-includes.
var secretRules = buildSecretRules()

// buildSecretRules assembles the built-in secret set once at package init: the
// slice is fixed, and [Secrets] hands out a copy.
func buildSecretRules() []Rule {
	var rules []Rule
	add := func(pattern string) {
		rules = append(rules, Rule{ID: SecretID, Pattern: pattern, Reason: SecretReason})
	}
	for _, p := range secretEnvPatterns {
		add(p)
	}
	for _, name := range secretNames {
		add(name)
		add("*/" + name)
	}
	for _, ext := range secretExts {
		add("*." + ext)
	}
	add("*.tfstate")
	add("*.tfstate.*")
	for _, template := range secretEnvTemplates {
		rules = append(rules,
			Rule{ID: SecretID, Pattern: template, Reason: SecretReason, Negate: true},
			Rule{ID: SecretID, Pattern: "*/" + template, Reason: SecretReason, Negate: true})
	}
	return rules
}

// Secrets returns the built-in secret exclusions in match order. The caller
// owns the slice: this is a clone, never the package's own list.
func Secrets() []Rule { return slices.Clone(secretRules) }

// SecretRule reports the built-in secret exclusion that withholds a path, or
// the template rule that cleared it, over a set the caller took from [Secrets]
// once per pass. Both sides of a rename are probed and either side matching
// means the section is withheld: renaming `.env` to `notes.txt` would otherwise
// publish the file's contents in the diff.
//
// Both sides are normalized first (`.`/`..` and doubled separators resolved,
// ASCII case folded), so `.ENV`, `Server.PEM`, `ID_RSA`, and `./keys//id_rsa`
// are withheld like their canonical spellings. A case-preserving match would
// make the floor a naming convention, and a rename to a secret in another
// letter case would slip through.
func SecretRule(rules []Rule, file, old string) (Rule, bool) {
	rule, drop := Excluded(rules, normalizeSecretPath(file), "")
	if !drop && old != "" {
		if oldRule, oldDrop := Excluded(rules, normalizeSecretPath(old), ""); oldDrop {
			return oldRule, true
		}
	}
	return rule, drop
}

// normalizeSecretPath folds one diff path to the spelling the built-in set
// matches: cleaned of `.`/`..` and repeated separators, then ASCII lowercase.
// A path the cleaner cannot express (the empty string) stays empty, which
// matches no secret rule.
func normalizeSecretPath(p string) string {
	if p == "" {
		return ""
	}
	return asciiLower(path.Clean(p))
}

// The package's `path` import is the stdlib cleaner; every matcher parameter is
// named for the diff side it carries, never `path`, so the two never shadow.

// asciiLower folds ASCII A-Z to a-z. The package is a leaf by design (it
// imports nothing internal), so it carries its own fold instead of reaching
// for the shared helper; a non-ASCII byte is left as it is, exactly like
// readcommon.ASCIILower.
func asciiLower(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
